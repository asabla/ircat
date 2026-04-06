package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LoadYAML decodes a Config from a YAML byte slice.
//
// Strategy: parse YAML into a generic Go value tree (map[string]any /
// []any / scalar) using our minimal in-tree parser, then re-encode as
// JSON and unmarshal into a Config so json struct tags do all the
// schema work. This keeps both decoders byte-for-byte consistent.
//
// The supported YAML subset is documented in [parseYAML].
func LoadYAML(data []byte) (*Config, error) {
	tree, err := parseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("decode yaml: %w", err)
	}
	if tree == nil {
		// Empty document — yield a zero Config so defaults can fill in.
		return &Config{}, nil
	}
	jsonBytes, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("yaml->json bridge: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode yaml into Config: %w", err)
	}
	return &cfg, nil
}
