package projectctx

import (
	"strings"
	"testing"
)

func TestDenyPatterns(t *testing.T) {
	got := DenyPatterns([]string{"  /Vendor/Keys/ ", "", "*.P12"})
	want := append(append([]string(nil), defaultDeny...), "vendor/keys", "*.p12")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("DenyPatterns = %q, want %q", got, want)
	}
}
