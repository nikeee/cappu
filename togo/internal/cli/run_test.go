package cli

import (
	"strings"
	"testing"
)

func TestSelectMainClass(t *testing.T) {
	// Configured mainClass always wins, even when detection is ambiguous or empty.
	if mc, reason := SelectMainClass([]string{"com.app.A", "com.app.B"}, "com.app.Chosen"); mc != "com.app.Chosen" || reason != "" {
		t.Errorf("configured: got (%q, %q)", mc, reason)
	}
	if mc, reason := SelectMainClass(nil, "com.app.Chosen"); mc != "com.app.Chosen" || reason != "" {
		t.Errorf("configured with no detection: got (%q, %q)", mc, reason)
	}
	// A single detected entry point is used.
	if mc, reason := SelectMainClass([]string{"com.app.Main"}, ""); mc != "com.app.Main" || reason != "" {
		t.Errorf("single: got (%q, %q)", mc, reason)
	}
	// None detected is an error.
	if mc, reason := SelectMainClass(nil, ""); mc != "" || !strings.Contains(reason, "no class declares a main") {
		t.Errorf("none: got (%q, %q)", mc, reason)
	}
	// Ambiguous detection lists the candidates.
	mc, reason := SelectMainClass([]string{"com.app.A", "com.app.B"}, "")
	if mc != "" || !strings.Contains(reason, "com.app.A, com.app.B") {
		t.Errorf("ambiguous: got (%q, %q)", mc, reason)
	}
}
