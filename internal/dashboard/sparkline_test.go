package dashboard

import (
	"strings"
	"testing"
)

func TestMetricSeries_PushAndSnapshot(t *testing.T) {
	s := &metricSeries{}
	for i := 1; i <= sparklineSamples+5; i++ {
		s.push(int64(i))
	}
	got := s.snapshot()
	if len(got) != sparklineSamples {
		t.Fatalf("snapshot len = %d, want %d", len(got), sparklineSamples)
	}
	// After overflowing, the oldest entries should be the
	// (5+1)..(5+sparklineSamples) range. The first kept value is
	// therefore 6.
	if got[0] != 6 {
		t.Errorf("first value = %d, want 6", got[0])
	}
	if got[len(got)-1] != int64(sparklineSamples+5) {
		t.Errorf("last value = %d, want %d", got[len(got)-1], sparklineSamples+5)
	}
}

func TestRenderSparkline_BelowTwoSamplesReturnsEmpty(t *testing.T) {
	srv := &Server{series: newMetricSeriesSet()}
	srv.series.push("x", 1)
	if got := srv.renderSparkline("x"); got != "" {
		t.Errorf("expected empty for 1 sample, got %q", got)
	}
}

func TestRenderSparkline_RisingSeriesProducesPolyline(t *testing.T) {
	srv := &Server{series: newMetricSeriesSet()}
	for i := 0; i < 10; i++ {
		srv.series.push("y", int64(i))
	}
	got := string(srv.renderSparkline("y"))
	if !strings.Contains(got, "<svg") || !strings.Contains(got, "polyline") {
		t.Fatalf("missing svg/polyline: %q", got)
	}
	// 10 samples → 10 "x,y" points separated by spaces.
	pointsStart := strings.Index(got, `points="`)
	if pointsStart < 0 {
		t.Fatalf("no points attr: %q", got)
	}
	pointsEnd := strings.Index(got[pointsStart+8:], `"`)
	points := got[pointsStart+8 : pointsStart+8+pointsEnd]
	parts := strings.Fields(points)
	if len(parts) != 10 {
		t.Errorf("polyline has %d points, want 10", len(parts))
	}
}

func TestRenderSparkline_ConstantSeriesIsCenterLine(t *testing.T) {
	srv := &Server{series: newMetricSeriesSet()}
	for i := 0; i < 5; i++ {
		srv.series.push("z", 42)
	}
	got := string(srv.renderSparkline("z"))
	// Every y coordinate should be the vertical center (14.0
	// for a 28-px high svg).
	if !strings.Contains(got, "14.0") {
		t.Errorf("constant series should center on y=14: %q", got)
	}
}
