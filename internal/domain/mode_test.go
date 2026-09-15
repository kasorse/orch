package domain

import "testing"

func TestParseRunMode(t *testing.T) {
	valid := []struct {
		in   string
		want RunMode
	}{
		{"plan", ModePlan},
		{"manual", ModeManual},
		{"accept-edits", ModeAcceptEdits},
		{"auto", ModeAuto},
	}
	for _, tt := range valid {
		got, err := ParseRunMode(tt.in)
		if err != nil {
			t.Errorf("ParseRunMode(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ParseRunMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	invalid := []string{"", "fast", "AUTO", "Plan"}
	for _, in := range invalid {
		if _, err := ParseRunMode(in); err == nil {
			t.Errorf("ParseRunMode(%q) expected error, got nil", in)
		}
	}
}
