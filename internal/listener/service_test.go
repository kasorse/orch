package listener

import (
	"errors"
	"strings"
	"testing"
)

func TestService(t *testing.T) {
	dirs := []string{"billing", "Auth", "web"}
	tests := []struct {
		name       string
		labels     []string
		components []string
		want       string
		wantErr    error
		errHas     []string
	}{
		{name: "one label", labels: []string{"ai-run", "service:billing"}, want: "billing"},
		{name: "one component", labels: []string{"ai-run"}, components: []string{"Billing"}, want: "billing"},
		{name: "a label and the same component", labels: []string{"service:billing"}, components: []string{"billing"}, want: "billing"},
		{name: "the same component in another case", labels: []string{"service:billing"}, components: []string{"BILLING"}, want: "billing"},
		{name: "a component without a directory is no service", labels: []string{"service:web"}, components: []string{"Docs"}, want: "web"},
		{name: "two names", labels: []string{"service:web"}, components: []string{"billing"}, wantErr: ErrManyServices, errHas: []string{"billing", "web"}},
		{name: "two labels", labels: []string{"service:web", "service:auth"}, wantErr: ErrManyServices, errHas: []string{"auth", "web"}},
		{name: "none", labels: []string{"ai-run"}, components: []string{"Docs"}, wantErr: ErrNoService},
		{name: "invalid", labels: []string{"service:../x"}, wantErr: ErrInvalidService, errHas: []string{"../x"}},
		{name: "empty", labels: []string{"service:"}, wantErr: ErrInvalidService},
		{name: "leading dot", labels: []string{"service:.clone-x"}, wantErr: ErrInvalidService},
		{name: "too long", labels: []string{"service:" + strings.Repeat("a", 65)}, wantErr: ErrInvalidService},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Service(tt.labels, tt.components, dirs, "service:")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Service = %q, %v; want %v", got, err, tt.wantErr)
				}
				for _, s := range tt.errHas {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q does not name %q", err, s)
					}
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Service = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestProject(t *testing.T) {
	tests := []struct {
		name    string
		labels  []string
		want    string
		wantErr error
	}{
		{name: "none", labels: []string{"ai-run", "service:x"}, want: ""},
		{name: "one", labels: []string{"gitlab:backend/billing"}, want: "backend/billing"},
		{name: "nested", labels: []string{"gitlab:platform/payments/api"}, want: "platform/payments/api"},
		{name: "git suffix", labels: []string{"gitlab:backend/billing.git"}, want: "backend/billing"},
		{name: "several", labels: []string{"gitlab:backend/a", "gitlab:backend/b"}, wantErr: ErrManyProjects},
		{name: "the same twice", labels: []string{"gitlab:backend/a", "gitlab:backend/a.git"}, want: "backend/a"},
		{name: "one segment", labels: []string{"gitlab:billing"}, wantErr: ErrInvalidProject},
		{name: "dot segment", labels: []string{"gitlab:backend/./x"}, wantErr: ErrInvalidProject},
		{name: "dotdot segment", labels: []string{"gitlab:backend/../x"}, wantErr: ErrInvalidProject},
		{name: "empty segment", labels: []string{"gitlab:backend//x"}, wantErr: ErrInvalidProject},
		{name: "leading slash", labels: []string{"gitlab:/backend/x"}, wantErr: ErrInvalidProject},
		{name: "bare git suffix segment", labels: []string{"gitlab:backend/.git"}, wantErr: ErrInvalidProject},
		{name: "space", labels: []string{"gitlab:backend/a b"}, wantErr: ErrInvalidProject},
		{name: "option", labels: []string{"gitlab:--upload-pack=x/y"}, want: "--upload-pack=x/y", wantErr: ErrInvalidProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Project(tt.labels, "gitlab:")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || got != "" {
					t.Fatalf("Project = %q, %v; want %v", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Project = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	groups := []string{"backend", "platform/payments"}
	tests := []struct {
		path string
		want bool
	}{
		{"backend/x", true},
		{"backend/x/y", true},
		{"platform/payments/api", true},
		{"backend-old/x", false},
		{"backend", false},
		{"platform/other", false},
		{"platform", false},
		{"platform/payments", false},
		{"backend/../x", false},
		{"backend//x", false},
		{"backend/./x", false},
		{"", false},
		{"Backend/x", false},
	}
	for _, tt := range tests {
		if got := Allowed(tt.path, groups); got != tt.want {
			t.Errorf("Allowed(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if Allowed("backend/x", nil) {
		t.Error("no groups allow nothing")
	}
}

func TestSameProject(t *testing.T) {
	tests := []struct {
		origin, base, path string
		want               bool
	}{
		{"https://h/g/p", "https://h", "g/p", true},
		{"https://h:443/g/p.git", "https://h", "g/p", true},
		{"https://H/g/p/", "https://h/", "g/p", true},
		{"https://h/g/p.git", "https://h:443", "g/p", true},
		{"HTTPS://h/g/p", "https://h", "g/p", true},
		{"https://h/ctx/g/p.git", "https://h/ctx", "g/p", true},
		{"https://h:8443/g/p", "https://h:8443", "g/p", true},
		{"http://localhost/g/p", "http://localhost:80", "g/p", true},
		{"file:///tmp/r/g/p.git", "file:///tmp/r", "g/p", true},
		{"https://other/g/p", "https://h", "g/p", false},
		{"https://h:8443/g/p", "https://h", "g/p", false},
		{"https://h/g/q", "https://h", "g/p", false},
		{"https://h/g/p/x", "https://h", "g/p", false},
		{"https://h/x/g/p", "https://h", "g/p", false},
		{"https://h/g/P", "https://h", "g/p", false},
		{"http://h/g/p", "https://h", "g/p", false},
		{"https://h/g/p", "https://h/ctx", "g/p", false},
		{"https://user:tok@h/g/p", "https://h", "g/p", false},
		{"https://h/g/p?x=1", "https://h", "g/p", false},
		{"git@h:g/p.git", "https://h", "g/p", false},
		{"", "https://h", "g/p", false},
		{"https://h/g/p", "", "g/p", false},
	}
	for _, tt := range tests {
		if got := SameProject(tt.origin, tt.base, tt.path); got != tt.want {
			t.Errorf("SameProject(%q, %q, %q) = %v, want %v", tt.origin, tt.base, tt.path, got, tt.want)
		}
	}
}

func TestEndpoint(t *testing.T) {
	a, ok := Endpoint("https", "GitLab.corp")
	b, ok2 := Endpoint("https", "gitlab.corp:443")
	if !ok || !ok2 || a != b {
		t.Errorf("Endpoint: %q %v, %q %v", a, ok, b, ok2)
	}
	if c, _ := Endpoint("https", "gitlab.corp:8443"); c == a {
		t.Error("another port is another endpoint")
	}
	if d, _ := Endpoint("http", "gitlab.corp"); d == a {
		t.Error("another protocol is another endpoint")
	}
	for _, host := range []string{"", "a/b", "u@h", "h h", "h?x"} {
		if _, ok := Endpoint("https", host); ok {
			t.Errorf("Endpoint(https, %q) accepted", host)
		}
	}
	if _, ok := Endpoint("", "h"); ok {
		t.Error("an empty protocol was accepted")
	}
}
