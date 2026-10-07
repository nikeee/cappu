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
// both scripts as a word of its own - a substring match would take the `test`
// in `testImplementation` for the `test` command - and every command word the
// scripts offer has to be one kong declares.
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
		words := map[string]bool{}
		inWord := func(r rune) bool {
			return r == '-' || r == '_' || r == '.' || r == '$' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		}
		for _, word := range strings.FieldsFunc(string(b), func(r rune) bool { return !inWord(r) }) {
			words[word] = true
		}
		for name := range declared {
			if !words[name] {
				t.Errorf("%s does not offer the %q command", shell, name)
			}
		}
		// The other direction: a command the scripts offer that kong does not
		// declare is one that was renamed or removed.
		for _, name := range commandsOffered(string(b)) {
			if !declared[name] {
				t.Errorf("%s offers %q, which is not a command", shell, name)
			}
		}
	}
}

// commandsOffered reads the commands a completion script offers: bash lists
// them all in the one `compgen -W` that holds `init`, zsh as `'name:what it
// does'` entries.
func commandsOffered(script string) []string {
	var names []string
	inCommands := false
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		// bash: every command on the one `compgen -W` that opens with `init`.
		if at := strings.Index(line, `compgen -W "`); at >= 0 {
			rest := line[at+len(`compgen -W "`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				if words := strings.Fields(rest[:end]); len(words) > 0 && words[0] == "init" {
					names = append(names, words...)
				}
			}
			continue
		}
		// zsh: the `commands=( 'name:what it does' .. )` array.
		switch {
		case trimmed == "commands=(":
			inCommands = true
		case inCommands && trimmed == ")":
			inCommands = false
		case inCommands && strings.HasPrefix(trimmed, "'"):
			name := strings.TrimPrefix(trimmed, "'")
			if at := strings.Index(name, ":"); at > 0 {
				names = append(names, name[:at])
			}
		}
	}
	return names
}
