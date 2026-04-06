package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LoadJSON decodes a Config from JSON bytes. It does not apply
// defaults, resolve env vars, or validate — use [Load] for the full
// pipeline.
func LoadJSON(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return &cfg, nil
}
