package secretscan

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// The secrets below are assembled at run time from harmless pieces: a literal token in the
// repository would trip secret scanners (and push protection) on this very file.
func rep(s string, n int) string { return strings.Repeat(s, n) }

var (
	awsKeyID     = "AK" + "IA" + "Z7Q2R5T8W1Y4B6C9"
	awsKeyID2    = "AS" + "IA" + "QWERTYUIOPASDFGH"
	awsSecret    = "aB3dE5gH7jK9mN1pQ3sT5vW7yZ9bC1eF3hJ5kL7n"
	githubPAT    = "gh" + "p_" + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	githubFine   = "github" + "_pat_" + rep("Ab1_", 21)
	gitlabToken  = "gl" + "pat-" + "x1Y2z3A4b5C6d7E8f9G0"
	slackToken   = "xo" + "xb-" + "1234567890-abcdefghij"
	anthropicKey = "sk-" + "ant-" + "api03-" + "Q1w2E3r4T5y6U7i8O9p0A1s2D3f4"
	openaiKey    = "sk-" + "proj-" + "Z9x8C7v6B5n4M3l2K1j0H9g8F7d6S5a4"
	stripeKey    = "sk" + "_live_" + "4eC39HqLyjWDarjtT1zdp7dc"
	jwtToken     = "ey" + "JhbGciOiJIUzI1NiJ9" + ".ey" + "JzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV"
	privateKey   = "-----BEGIN " + "RSA PRIVATE KEY-----"
	randomSecret = "Zq8#vL2!pW9$mK4@tR7"
)

type expected struct {
	line string
	rule string // "" = no finding
}

func TestScan_Rules(t *testing.T) {
	cases := []expected{
		// aws-access-key-id
		{`key := "` + awsKeyID + `"`, "aws-access-key-id"},
		{`AWS_ACCESS_KEY_ID=` + awsKeyID2, "aws-access-key-id"},
		{`id = "AKIA` + "Z7Q2R5T8W1Y4B6C" + `"`, ""},        // 15 characters
		{`AWS_ACCESS_KEY_ID=AKIA` + "IOSFODNN7EXAMPLE", ""}, // the documentation example
		// aws-secret-key
		{`aws_secret_access_key = "` + awsSecret + `"`, "aws-secret-key"},
		{`AWS_SK: '` + awsSecret + `'`, "aws-secret-key"},
		{`aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`, ""},
		{`aws_region = "` + awsSecret[:20] + `"`, ""},
		// private-key
		{privateKey, "private-key"},
		{"  " + strings.Replace(privateKey, "RSA ", "", 1), "private-key"},
		{"-----BEGIN PUBLIC KEY-----", ""},
		{"-----BEGIN CERTIFICATE-----", ""},
		// github-token
		{`token: ` + githubPAT, "github-token"},
		{githubFine, "github-token"},
		{"ghp_" + "short", ""},
		{"github.com/ghost_user/repo", ""},
		// gitlab-token
		{`GITLAB=` + gitlabToken, "gitlab-token"},
		{`"` + gitlabToken + `"`, "gitlab-token"},
		{"glpat-" + "short", ""},
		{"see glpat docs", ""},
		// slack-token
		{slackToken, "slack-token"},
		{"xo" + "xp-" + "9876543210-zyxwvutsrq", "slack-token"},
		{"xoxb-1", ""},
		{"xoxo kisses", ""},
		// anthropic-key (reported once, not also as openai-key)
		{`ANTHROPIC_API_KEY=` + anthropicKey, "anthropic-key"},
		{`"` + anthropicKey + `"`, "anthropic-key"},
		{"sk-ant-" + "short", ""},
		{"ask-ant-of-the-day", ""},
		// openai-key
		{openaiKey, "openai-key"},
		{"sk-" + rep("a1B2", 9), "openai-key"},
		{"sk-" + "too-short", ""},
		{"disk-usage-report-for-the-whole-cluster", ""},
		// stripe-key
		{stripeKey, "stripe-key"},
		{"rk" + "_live_" + "abcdefghijklmnopqrstu", "stripe-key"},
		{"sk_test_4eC39HqLyjWDarjtT1zdp7dc", ""},
		{"sk_live_short", ""},
		// jwt
		{"Authorization: Bearer " + jwtToken, "jwt"},
		{jwtToken, "jwt"},
		{"eyJhbGc.eyJzdWI.sig", ""},
		{"eyJust a word", ""},
		// url-credentials
		{"postgres://app:" + "s3cr3t-Pa55@db.internal:5432/app", "url-credentials"},
		{"https://deploy:" + "tok_93jf83@git.example.org/x.git", "url-credentials"},
		{"postgres://user:password@localhost:5432/test", ""},
		{"https://user:${DB_PASSWORD}@host/db", ""},
		{"https://example.org/path", ""},
		// generic-assignment
		{`password = "` + randomSecret + `"`, "generic-assignment"},
		{`clientSecret: '` + randomSecret + `'`, "generic-assignment"},
		{"API_KEY := `" + randomSecret + "`", "generic-assignment"},
		{`password = "aaaaaaaaaaaaaaaaaaaa"`, ""},               // one character repeated
		{`password = "passwordpassword"`, ""},                   // low entropy
		{`password: "${DB_PASSWORD}"`, ""},                      // template
		{`token = os.Getenv("TOKEN") + "Zq8#vL2!pW9$mK4@"`, ""}, // env lookup
		{`secret = "<your-secret-here-please>"`, ""},
		{`api_key = "example-` + randomSecret + `"`, ""},
		{`password = "short"`, ""},
		{`const DefaultClaudeAPIKeyEnv = "ANTHROPIC_API_KEY"`, ""}, // names a variable
		{`api_key_env: "OPENAI_API_KEY_V2"`, ""},
		{`secret = "` + "JBSWY3DP" + "EHPK3PXPQ4ZA" + `"`, "generic-assignment"}, // base32, no underscore; split so this file has no finding
	}
	for _, tc := range cases {
		got := ScanLines("cfg/app.go", []NumberedLine{{Number: 7, Text: tc.line}})
		var rules []string
		for _, f := range got {
			rules = append(rules, f.Rule)
			if f.Line != 7 {
				t.Errorf("%q: line %d, want 7", tc.line, f.Line)
			}
		}
		want := []string{}
		if tc.rule != "" {
			want = []string{tc.rule}
		}
		if len(rules) == 0 {
			rules = []string{}
		}
		if !slices.Equal(rules, want) {
			t.Errorf("%q: rules %v, want %v", tc.line, rules, want)
		}
	}
}

// Every rule is exercised by the table above, so a new rule cannot ship untested.
func TestScan_EveryRuleHasCases(t *testing.T) {
	covered := map[string]bool{}
	for _, l := range []string{awsKeyID, `aws_secret_access_key = "` + awsSecret + `"`, privateKey, githubPAT, gitlabToken,
		slackToken, anthropicKey, openaiKey, stripeKey, jwtToken, "postgres://app:s3cr3t-Pa55@db/app", `password = "` + randomSecret + `"`} {
		for _, f := range ScanLines("x", []NumberedLine{{1, l}}) {
			covered[f.Rule] = true
		}
	}
	for _, id := range RuleIDs() {
		if !covered[id] {
			t.Errorf("rule %s has no positive case", id)
		}
	}
}

func TestScan_FileLinesSkipsAndFindsNothingInPlainCode(t *testing.T) {
	content := "package cfg\n\nconst ID = \"" + awsKeyID + "\"\n\n// " + githubPAT + "\n"
	got, skipped := Scan("cfg/ids.go", []byte(content))
	if skipped || len(got) != 2 || got[0].Line != 3 || got[0].Rule != "aws-access-key-id" || got[1].Line != 5 || got[1].Rule != "github-token" {
		t.Errorf("Scan = %+v, skipped %v", got, skipped)
	}

	plain := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ttoken := tokenizer.Next()\n\tfmt.Println(token, \"secret santa\")\n}\n"
	if got, _ := Scan("main.go", []byte(plain)); len(got) != 0 {
		t.Errorf("plain code reported: %+v", got)
	}

	if _, skipped := Scan("logo.png", []byte("\x89PNG\x00"+awsKeyID)); !skipped {
		t.Error("a binary file was scanned")
	}
	if _, skipped := Scan("big.txt", []byte(strings.Repeat("x", MaxFileSize+1))); !skipped {
		t.Error("a file over MaxFileSize was scanned")
	}
}

func TestFingerprint_StableAcrossMovesNotAcrossFiles(t *testing.T) {
	a, _ := Scan("a.go", []byte("x\n"+awsKeyID+"\n"))
	moved, _ := Scan("a.go", []byte(strings.Repeat("\n", 10)+awsKeyID+"\n"))
	other, _ := Scan("b.go", []byte(awsKeyID+"\n"))
	if len(a) != 1 || len(moved) != 1 || len(other) != 1 {
		t.Fatalf("findings: %v %v %v", a, moved, other)
	}
	if a[0].Fingerprint != moved[0].Fingerprint {
		t.Error("the fingerprint changed when the line moved")
	}
	if a[0].Fingerprint == other[0].Fingerprint {
		t.Error("the same value in another file has the same fingerprint")
	}
	if len(a[0].Fingerprint) != 64 {
		t.Errorf("fingerprint %q is not hex sha256", a[0].Fingerprint)
	}
}

// Nothing printed from a Finding contains the value it found.
func TestFinding_NeverCarriesTheValue(t *testing.T) {
	secrets := []string{awsKeyID, githubPAT, anthropicKey, stripeKey, randomSecret, "s3cr3t-Pa55"}
	var content strings.Builder
	for _, s := range []string{awsKeyID, githubPAT, anthropicKey, stripeKey} {
		content.WriteString(s + "\n")
	}
	content.WriteString(`password = "` + randomSecret + "\"\npostgres://app:s3cr3t-Pa55@db/app\n")
	findings, _ := Scan("x.go", []byte(content.String()))
	if len(findings) != 6 {
		t.Fatalf("findings = %v", findings)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(format, findings)
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("%s of the findings contains a value", format)
			}
		}
	}
}

func TestRedact(t *testing.T) {
	text := "+key := \"" + awsKeyID + "\"\n" +
		"+" + privateKey + "\n+MIIEowIBAAKCAQEA\n+-----END RSA PRIVATE KEY-----\n" +
		" unchanged context line\n" +
		"+url = \"postgres://app:s3cr3t-Pa55@db/app\"\n" +
		"+password = \"" + randomSecret + "\"\n"
	got := Redact(text)
	for _, secret := range []string{awsKeyID, "MIIEowIBAAKCAQEA", "s3cr3t-Pa55", randomSecret} {
		if strings.Contains(got, secret) {
			t.Errorf("Redact left %q:\n%s", secret, got)
		}
	}
	for _, want := range []string{"[REDACTED:aws-access-key-id]", "[REDACTED:private-key]", "postgres://app:[REDACTED:url-credentials]@db/app",
		"password = \"[REDACTED:generic-assignment]\"", " unchanged context line\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("Redact lacks %q:\n%s", want, got)
		}
	}

	// A key cut off before its END line is redacted to the end.
	if got := Redact("+" + privateKey + "\n+MIIEowIBAAKCAQEA\n"); strings.Contains(got, "MIIE") {
		t.Errorf("a truncated key block was left: %q", got)
	}
}

// Redact and Scan agree: every positive is removed, every negative left alone.
func TestRedact_AgreesWithScan(t *testing.T) {
	for _, l := range []string{awsKeyID, githubPAT, gitlabToken, slackToken, anthropicKey, openaiKey, stripeKey, jwtToken} {
		if got := Redact(l); strings.Contains(got, l) || ScanLines("x", []NumberedLine{{1, got}}) != nil {
			t.Errorf("Redact(%q) = %q", l, got)
		}
	}
	for _, l := range []string{"AKIAIOSFODNN7EXAMPLE", "sk_test_4eC39HqLyjWDarjtT1zdp7dc", `password = "${DB_PASSWORD}"`, "https://example.org/path"} {
		if got := Redact(l); got != l {
			t.Errorf("Redact changed a non-secret: %q -> %q", l, got)
		}
	}
}

func TestEntropy(t *testing.T) {
	if e := entropy("aaaa"); e != 0 {
		t.Errorf("entropy(aaaa) = %v", e)
	}
	if e := entropy("ab"); e != 1 {
		t.Errorf("entropy(ab) = %v", e)
	}
	if e := entropy(randomSecret); e < minEntropy {
		t.Errorf("entropy(randomSecret) = %v, below the threshold", e)
	}
}

// A megabyte of ordinary Go source scans quickly: no rule may backtrack its way into
// seconds on a large generated file.
func TestScan_Fast(t *testing.T) {
	line := "\tvalue, err := strconv.Atoi(token) // parse the secret-free key of the map entry\n"
	content := []byte(strings.Repeat(line, MaxFileSize/len(line)))
	start := time.Now()
	Scan("big.go", content)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("scanning 1 MiB took %s", d)
	}
}

func BenchmarkScan1MiB(b *testing.B) {
	// The worst ordinary line: hints for several rules and a quoted assignment.
	line := "\ttokenKey := cfg.Get(\"secret-free key of the map entry, not a password\")\n"
	content := []byte(strings.Repeat(line, MaxFileSize/len(line)))
	b.SetBytes(int64(len(content)))
	for b.Loop() {
		Scan("big.go", content)
	}
}

// The hand-written shape check never rejects a line the generic pattern would match.
func FuzzAssignmentShape(f *testing.F) {
	f.Add(`password = "` + randomSecret + `"`)
	f.Add("API-KEY\r:\t'abcdefghijklmnopq'")
	f.Add(`x.private_key": "abcdefghijklmnopqrstu"`)
	f.Add("tokenKey := cfg.Get(\"x\")")
	var generic *rule
	for i := range rules {
		if rules[i].id == "generic-assignment" {
			generic = &rules[i]
		}
	}
	f.Fuzz(func(t *testing.T, line string) {
		if generic.re.MatchString(line) && !assignmentShape(line) {
			t.Fatalf("the pattern matches %q but the shape check rejects it", line)
		}
	})
}
