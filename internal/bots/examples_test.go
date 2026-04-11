package bots

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExampleBotsValidate makes sure every shipped example in
// examples/bots/*.lua parses cleanly through the same Validate
// path the dashboard's pre-save check uses. If we add a broken
// example, CI catches it instead of an operator pasting it into
// production and hitting a compile error.
func TestExampleBotsValidate(t *testing.T) {
	root, err := filepath.Abs("../../examples/bots")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read examples dir %s: %v", root, err)
	}
	var found int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lua" {
			continue
		}
		found++
		path := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if err := Validate(string(src)); err != nil {
				t.Fatalf("example %s failed to validate: %v", e.Name(), err)
			}
		})
	}
	if found == 0 {
		t.Fatalf("no .lua examples found under %s", root)
	}
}
