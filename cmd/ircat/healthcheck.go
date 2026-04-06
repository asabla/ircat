package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// runHealthcheck performs a single HTTP GET against the given address
// and exits with the corresponding status. It is invoked by the
// production docker-compose healthcheck so the production image does
// not need curl/wget baked in (the runtime is distroless/static).
func runHealthcheck(args []string) error {
	fs := flag.NewFlagSet("ircat healthcheck", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		address string
		timeout time.Duration
	)
	fs.StringVar(&address, "address", "http://127.0.0.1:8080/healthz", "URL to probe")
	fs.DurationVar(&timeout, "timeout", 3*time.Second, "request timeout")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: ircat healthcheck [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", address, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("probe %s: status %d", address, resp.StatusCode)
	}
	return nil
}
