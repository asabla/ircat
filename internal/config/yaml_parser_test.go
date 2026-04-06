package config

import (
	"reflect"
	"testing"
)

func TestParseYAML_FlatMapping(t *testing.T) {
	got, err := parseYAML([]byte("a: 1\nb: hello\nc: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a": int64(1),
		"b": "hello",
		"c": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseYAML_NestedMapping(t *testing.T) {
	in := `
server:
  name: irc.local
  limits:
    max_clients: 100
    nick_length: 30
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"server": map[string]any{
			"name": "irc.local",
			"limits": map[string]any{
				"max_clients": int64(100),
				"nick_length": int64(30),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseYAML_SequenceOfScalars(t *testing.T) {
	in := `
allow_origins:
  - https://a.example
  - https://b.example
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"allow_origins": []any{"https://a.example", "https://b.example"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseYAML_SequenceOfMappings(t *testing.T) {
	in := `
listeners:
  - address: "0.0.0.0:6667"
    tls: false
  - address: "0.0.0.0:6697"
    tls: true
    cert_file: /tls/cert.pem
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"listeners": []any{
			map[string]any{
				"address": "0.0.0.0:6667",
				"tls":     false,
			},
			map[string]any{
				"address":   "0.0.0.0:6697",
				"tls":       true,
				"cert_file": "/tls/cert.pem",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseYAML_EmptyFlowMarkers(t *testing.T) {
	got, err := parseYAML([]byte("links: []\nmeta: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"links": []any{},
		"meta":  map[string]any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseYAML_QuotedStrings(t *testing.T) {
	in := `
plain: hello world
double: "with: colon and \"quotes\""
single: 'it''s fine'
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["plain"] != "hello world" {
		t.Errorf("plain = %v", m["plain"])
	}
	if m["double"] != `with: colon and "quotes"` {
		t.Errorf("double = %v", m["double"])
	}
	if m["single"] != "it's fine" {
		t.Errorf("single = %v", m["single"])
	}
}

func TestParseYAML_Comments(t *testing.T) {
	in := `
# leading comment
a: 1   # trailing
b: "url#fragment"   # the # in the value is preserved
# trailing comment
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["a"] != int64(1) {
		t.Errorf("a = %v", m["a"])
	}
	if m["b"] != "url#fragment" {
		t.Errorf("b = %v", m["b"])
	}
}

func TestParseYAML_NullsAndBools(t *testing.T) {
	in := "a: null\nb: ~\nc: yes\nd: NO\ne: true\n"
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["a"] != nil || m["b"] != nil {
		t.Errorf("nulls = %v %v", m["a"], m["b"])
	}
	if m["c"] != true || m["d"] != false || m["e"] != true {
		t.Errorf("bools = %v %v %v", m["c"], m["d"], m["e"])
	}
}

func TestParseYAML_TabsRejected(t *testing.T) {
	if _, err := parseYAML([]byte("a:\n\tb: 1\n")); err == nil {
		t.Fatal("expected tab rejection")
	}
}

func TestParseYAML_DocumentMarker(t *testing.T) {
	got, err := parseYAML([]byte("---\nfoo: bar\n"))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["foo"] != "bar" {
		t.Errorf("foo = %v", m["foo"])
	}
}

func TestParseYAML_DuplicateKey(t *testing.T) {
	if _, err := parseYAML([]byte("a: 1\na: 2\n")); err == nil {
		t.Fatal("expected duplicate-key error")
	}
}

func TestParseYAML_DeepNesting(t *testing.T) {
	in := `
events:
  sinks:
    - type: webhook
      url: https://hooks.example.org/x
      retry:
        max_attempts: 5
        backoff_seconds:
          - 1
          - 2
          - 5
    - type: jsonl
      path: /var/log/events.jsonl
`
	got, err := parseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	root := got.(map[string]any)
	sinks := root["events"].(map[string]any)["sinks"].([]any)
	if len(sinks) != 2 {
		t.Fatalf("sinks len = %d", len(sinks))
	}
	first := sinks[0].(map[string]any)
	if first["type"] != "webhook" {
		t.Errorf("first.type = %v", first["type"])
	}
	retry := first["retry"].(map[string]any)
	if retry["max_attempts"] != int64(5) {
		t.Errorf("retry.max_attempts = %v", retry["max_attempts"])
	}
	backoff := retry["backoff_seconds"].([]any)
	if len(backoff) != 3 || backoff[2] != int64(5) {
		t.Errorf("backoff = %v", backoff)
	}
}

func TestParseYAML_Empty(t *testing.T) {
	got, err := parseYAML([]byte(""))
	if err != nil || got != nil {
		t.Errorf("empty: got %v err %v", got, err)
	}
	got, err = parseYAML([]byte("# only comments\n\n"))
	if err != nil || got != nil {
		t.Errorf("comment-only: got %v err %v", got, err)
	}
}
