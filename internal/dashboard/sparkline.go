package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"strings"
	"sync"
	"time"
)

// sparklineSamples is the number of historical samples kept per
// metric. At a 5-second sample interval that is 5 minutes of
// history, which is enough for an at-a-glance "is this metric
// climbing right now?" read without burning host memory on
// per-card history nobody looks at.
const sparklineSamples = 60

// sparklineInterval is how often the dashboard's sample loop
// pushes a new value into the rolling buffer. Picked to match
// the htmx auto-refresh interval on the overview page so the
// numbers and the sparkline trace stay in lock-step.
const sparklineInterval = 5 * time.Second

// metricSeries is the per-metric ring buffer the sparklines
// render from. Implemented as a fixed-size slice + an index so
// pushes are O(1) and the read path is a single copy under the
// mutex.
type metricSeries struct {
	mu     sync.Mutex
	values [sparklineSamples]int64
	count  int
}

// push appends v to the series, evicting the oldest entry once
// the buffer is full. Safe for concurrent callers.
func (s *metricSeries) push(v int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count < sparklineSamples {
		s.values[s.count] = v
		s.count++
		return
	}
	copy(s.values[:], s.values[1:])
	s.values[sparklineSamples-1] = v
}

// snapshot copies the current values out under the lock so the
// renderer can walk them without holding the series mutex.
func (s *metricSeries) snapshot() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, s.count)
	copy(out, s.values[:s.count])
	return out
}

// metricSeriesSet is the dashboard-wide collection of per-card
// rolling buffers. Each known card name maps to its own series.
// Lookups are read-locked so the htmx fragment handler does not
// race the sample loop.
type metricSeriesSet struct {
	mu     sync.RWMutex
	series map[string]*metricSeries
}

func newMetricSeriesSet() *metricSeriesSet {
	return &metricSeriesSet{series: make(map[string]*metricSeries)}
}

// push adds v to the named series, creating it on first use.
func (m *metricSeriesSet) push(name string, v int64) {
	m.mu.Lock()
	s := m.series[name]
	if s == nil {
		s = &metricSeries{}
		m.series[name] = s
	}
	m.mu.Unlock()
	s.push(v)
}

// snapshot returns a copy of the named series' values, or nil
// if the series does not exist yet.
func (m *metricSeriesSet) snapshot(name string) []int64 {
	m.mu.RLock()
	s := m.series[name]
	m.mu.RUnlock()
	if s == nil {
		return nil
	}
	return s.snapshot()
}

// startSampleLoop runs a background goroutine that polls the
// metrics source every sparklineInterval and pushes the
// resulting values into the matching series. Stops when ctx is
// cancelled.
func (s *Server) startSampleLoop(ctx context.Context) {
	if s.metrics == nil {
		return
	}
	if s.series == nil {
		s.series = newMetricSeriesSet()
	}
	go func() {
		t := time.NewTicker(sparklineInterval)
		defer t.Stop()
		// Take an immediate first sample so the first render
		// after a reboot does not start with an empty buffer.
		s.sampleMetrics()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sampleMetrics()
			}
		}
	}()
}

func (s *Server) sampleMetrics() {
	if s.metrics == nil || s.series == nil {
		return
	}
	s.series.push("users", int64(s.metrics.UserCount()))
	s.series.push("channels", int64(s.metrics.ChannelCount()))
	s.series.push("federation links", int64(s.metrics.FederationLinkCount()))
	s.series.push("bots", int64(s.metrics.BotCount()))
	s.series.push("messages in", int64(s.metrics.MessagesIn()))
	s.series.push("messages out", int64(s.metrics.MessagesOut()))
}

// renderSparkline returns an inline SVG fragment of the named
// series, sized to the dashboard card. Returns the empty string
// when there are fewer than two samples (one point cannot draw
// a line). The result is marked template.HTML so the caller
// can drop it straight into the card without re-escaping.
func (s *Server) renderSparkline(name string) template.HTML {
	if s.series == nil {
		return ""
	}
	values := s.series.snapshot(name)
	if len(values) < 2 {
		return ""
	}
	const w, h = 80, 28
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rangeY := max - min
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="spark" viewBox="0 0 %d %d" preserveAspectRatio="none">`, w, h)
	b.WriteString(`<polyline fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" points="`)
	stride := float64(w) / float64(len(values)-1)
	for i, v := range values {
		x := float64(i) * stride
		var y float64
		if rangeY == 0 {
			y = float64(h) / 2
		} else {
			y = float64(h) - float64(v-min)/float64(rangeY)*float64(h-2) - 1
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}
	b.WriteString(`" /></svg>`)
	return template.HTML(b.String())
}
