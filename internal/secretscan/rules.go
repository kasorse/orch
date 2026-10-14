package secretscan

import (
	"regexp"
	"strings"
)

// rule is one kind of secret. The value is the whole match, or capture group 1 when the
// pattern has one (the password in a URL, the literal in an assignment): that is what a
// Finding's fingerprint is taken over and what Redact replaces. A rule is tried on a line
// only when one of its hints occurs in it, which keeps a scan of a large file cheap.
type rule struct {
	id string
	re *regexp.Regexp
	// hints are substrings one of which a line must contain; foldHints compares them
	// against the lower-cased line.
	hints     []string
	foldHints bool
	// pre, when set, is a cheaper test a line must also pass before the pattern runs.
	pre func(line string) bool
	// accept, when set, has the last word on a candidate value and its line.
	accept func(value, line string) bool
	// redact, when set, is the pattern Redact uses instead of re - for a private key, the
	// whole block, not only its first line.
	redact *regexp.Regexp
}

// rules is every rule, in reporting order (specs/hardening/SPEC.md §9.2).
var rules = []rule{
	{
		id: "aws-access-key-id", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		hints:  []string{"AKIA", "ASIA"},
		accept: func(v, _ string) bool { return !awsExamples[v] },
	},
	{
		id: "aws-secret-key", re: regexp.MustCompile(`(?i)aws.{0,20}(?:secret|sk).{0,20}['"]([0-9a-zA-Z/+]{40})['"]`),
		hints: []string{"aws"}, foldHints: true,
		accept: func(v, _ string) bool { return !awsExamples[v] },
	},
	{
		id: "private-key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		hints: []string{"PRIVATE KEY"},
		// The block up to its END line - or, cut off, to the end of the text.
		redact: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?:[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----|[\s\S]*)`),
	},
	{
		id: "github-token", re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{80,}\b`),
		hints: []string{"gh", "github_pat_"},
	},
	{id: "gitlab-token", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), hints: []string{"glpat-"}},
	{id: "slack-token", re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`), hints: []string{"xox"}},
	{id: "anthropic-key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), hints: []string{"sk-ant-"}},
	{
		id: "openai-key", re: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}`),
		hints: []string{"sk-"},
		// An Anthropic key is reported by its own rule, not twice.
		accept: func(v, _ string) bool { return !strings.HasPrefix(v, "sk-ant-") },
	},
	{id: "stripe-key", re: regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{20,}`), hints: []string{"_live_"}},
	{
		id: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
		hints: []string{"eyJ"},
	},
	{
		id: "url-credentials", re: regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@'"]+:([^/\s:@'"]{3,})@`),
		hints:  []string{"://"},
		accept: func(v, _ string) bool { return !placeholder(v) && !commonURLPasswords[strings.ToLower(v)] },
	},
	{
		id: "generic-assignment",
		re: regexp.MustCompile(`(?i)(?:password|passwd|secret|token|api[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*["']?\s*(?::=|=|:)\s*` +
			"(?:\"([^\"\\n]{16,})\"|'([^'\\n]{16,})'|`([^`\\n]{16,})`)"),
		hints:     []string{"password", "passwd", "secret", "token", "key"},
		foldHints: true,
		// The pattern's shape checked by hand first: most lines that merely mention a token
		// or a key are not an assignment of a quoted literal, and skip the pattern.
		pre: assignmentShape,
		accept: func(v, line string) bool {
			return entropy(v) >= minEntropy && !placeholder(v) && !envLookup(line) && !envVarName.MatchString(v)
		},
	},
}

// awsExamples are the keys AWS documentation uses; they appear in many codebases.
var awsExamples = map[string]bool{
	"AKIAIOSFODNN7EXAMPLE":                     true,
	"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY": true,
}

// commonURLPasswords are what documentation and local test setups put in a connection URL.
var commonURLPasswords = map[string]bool{
	"password": true, "pass": true, "passwd": true, "secret": true, "test": true, "user": true,
	"admin": true, "root": true, "postgres": true, "guest": true,
}

// minEntropy is the Shannon entropy, in bits per character, a generic-assignment value
// needs: real secrets are random, hand-written strings are not.
const minEntropy = 3.5

// placeholderWords mark a value as a stand-in, not a secret.
var placeholderWords = []string{"example", "changeme", "dummy", "fake", "placeholder", "xxxx", "redacted", "your_", "your-"}

// placeholder reports a value that stands in for a secret: a template (${...}, <...>,
// {{...}}), a shell variable, one character repeated, or a value containing a placeholder
// word.
func placeholder(v string) bool {
	lower := strings.ToLower(v)
	for _, w := range placeholderWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	if strings.HasPrefix(v, "$") || strings.Contains(v, "${") || strings.Contains(v, "{{") ||
		(strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">")) {
		return true
	}
	return v == "" || strings.Count(v, v[:1]) == len(v)
}

// envVarName is a value that names an environment variable (api_key_env: "ANTHROPIC_API_KEY"):
// upper-case segments joined by underscores. At least one underscore is required, so a base32
// secret (upper-case letters and digits only) still counts.
var envVarName = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+$`)

// envLookups on a line mean the value is read from the environment, not written down.
var envLookups = []string{"os.Getenv(", "os.LookupEnv(", "process.env.", "os.environ", "os.getenv(", "System.getenv(", "ENV["}

func envLookup(line string) bool {
	for _, l := range envLookups {
		if strings.Contains(line, l) {
			return true
		}
	}
	return false
}

// assignmentKeywords are generic-assignment's name keywords, lower-case.
var assignmentKeywords = []string{"password", "passwd", "secret", "token", "apikey", "api_key", "api-key", "privatekey", "private_key", "private-key"}

// assignmentShape reports whether line has a keyword followed by name characters, an
// optional closing quote, "=", ":=" or ":", and an opening quote - generic-assignment's
// structure without its cost.
func assignmentShape(line string) bool {
	lower := strings.ToLower(line)
	for _, k := range assignmentKeywords {
		for from := 0; ; {
			i := strings.Index(lower[from:], k)
			if i < 0 {
				break
			}
			pos := from + i + len(k)
			from = pos
			for pos < len(lower) && isNameByte(lower[pos]) {
				pos++
			}
			if pos < len(lower) && (lower[pos] == '"' || lower[pos] == '\'') {
				pos++
			}
			pos = skipSpaces(lower, pos)
			switch {
			case strings.HasPrefix(lower[pos:], ":="):
				pos += 2
			case pos < len(lower) && (lower[pos] == '=' || lower[pos] == ':'):
				pos++
			default:
				continue
			}
			pos = skipSpaces(lower, pos)
			if pos < len(lower) && strings.IndexByte("\"'`", lower[pos]) >= 0 {
				return true
			}
		}
	}
	return false
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

func skipSpaces(s string, pos int) int {
	// What \s matches in RE2.
	for pos < len(s) && strings.IndexByte(" \t\n\r\f\v", s[pos]) >= 0 {
		pos++
	}
	return pos
}
