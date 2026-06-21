package cli

import "testing"

func TestAgentEnabled(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"empty", map[string]string{}, false},
		{"AGENT name", map[string]string{"AGENT": "goose"}, true},
		{"AGENT 1", map[string]string{"AGENT": "1"}, true},
		{"CLAUDECODE", map[string]string{"CLAUDECODE": "1"}, true},
		{"CURSOR_AGENT", map[string]string{"CURSOR_AGENT": "1"}, true},
		{"CODEX_SANDBOX", map[string]string{"CODEX_SANDBOX": "seatbelt"}, true},
		{"TRAE session", map[string]string{"TRAE_AI_SHELL_ID": "abc123"}, true},
		{"set but empty", map[string]string{"AGENT": ""}, false},
		{"unrelated", map[string]string{"SHELL": "/bin/zsh"}, false},
	}
	for _, c := range cases {
		env := func(name string) string { return c.env[name] }
		if got := AgentEnabled(env); got != c.want {
			t.Errorf("%s: AgentEnabled() = %v, want %v", c.name, got, c.want)
		}
	}
}
