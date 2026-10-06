package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The completion scripts are checked-in assets the binary embeds, so nothing
// regenerates them when a command is added: this is the guard that was the
// TypeScript generator's job. Every command kong declares has to appear in
// both scripts, and every command they offer has to exist.
func TestCompletionScriptsListEveryCommand(t *testing.T) {
	declared := map[string]bool{}
	fields := reflect.VisibleFields(reflect.TypeOf(CLI{}))
	for _, field := range fields {
		if _, ok := field.Tag.Lookup("cmd"); !ok {
			continue
		}
		name := field.Tag.Get("name")
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		declared[name] = true
	}
	if len(declared) == 0 {
		t.Fatal("no commands found on CLI")
	}
	for _, shell := range []string{"cappu.bash", "cappu.zsh"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "cli", "completion", shell))
		if err != nil {
			t.Fatalf("read %s: %v", shell, err)
		}
		script := string(b)
		for name := range declared {
			if !strings.Contains(script, name) {
				t.Errorf("%s does not offer the %q command", shell, name)
			}
		}
	}
}
