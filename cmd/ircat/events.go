package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/events"
)

// buildEventBus constructs an events.Bus from the configured sinks
// and subscribes each one. Unknown sink types produce an error so
// a typo in cfg is surfaced at startup rather than silently
// dropping events.
//
// Returns the bus + a slice of sink handles the caller must Close
// on shutdown. The caller typically chains Close through
// bus.Close() which already walks the subscribers, so the handles
// are mostly informational.
func buildEventBus(cfg *config.Config, logger *slog.Logger) (*events.Bus, error) {
	bus := events.NewBus(logger.With("component", "events"))
	for i, spec := range cfg.Events.Sinks {
		if spec.Enabled != nil && !*spec.Enabled {
			continue
		}
		sink, err := buildSink(spec)
		if err != nil {
			_ = bus.Close()
			return nil, fmt.Errorf("events sink %d (%s): %w", i, spec.Type, err)
		}
		bus.Subscribe(sink, events.SubscribeOptions{})
		logger.Info("event sink subscribed", "type", spec.Type, "index", i)
	}
	return bus, nil
}

// buildSink constructs one events.Sink from a config.SinkConfig.
// Unknown types surface as an error.
func buildSink(spec config.SinkConfig) (events.Sink, error) {
	switch spec.Type {
	case "jsonl":
		return events.NewJSONLSink(events.JSONLSinkOptions{
			Path:        spec.Path,
			RotateBytes: int64(spec.RotateMB) * 1024 * 1024,
			Keep:        spec.Keep,
		})
	case "webhook":
		opts := events.WebhookSinkOptions{
			URL:            spec.URL,
			Secret:         spec.Secret,
			DeadLetterPath: spec.DeadLetterPath,
		}
		if spec.TimeoutSeconds > 0 {
			opts.Timeout = time.Duration(spec.TimeoutSeconds) * time.Second
		}
		if spec.Retry != nil {
			opts.MaxRetries = spec.Retry.MaxAttempts
			if len(spec.Retry.BackoffSeconds) > 0 {
				schedule := spec.Retry.BackoffSeconds
				opts.Backoff = func(attempt int) time.Duration {
					if attempt >= len(schedule) {
						return time.Duration(schedule[len(schedule)-1]) * time.Second
					}
					return time.Duration(schedule[attempt]) * time.Second
				}
			}
		}
		return events.NewWebhookSink(opts)
	}
	return nil, fmt.Errorf("unknown sink type %q (want jsonl or webhook)", spec.Type)
}
