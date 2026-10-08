package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"orchbench/internal/exitcode"
)

// bench/README.md is the manual of orchbench (specs/benchmark/tasks/TASK-BM14-documentation.md):
// it must list exactly the commands and flags `orchbench --help` does, and each command's
// synopsis as the help prints it.

const readmePath = "../../README.md"

var (
	readmeCommandHeading = regexp.MustCompile("(?m)^### `orchbench ([a-z]+)`$")
	// readmeFlagRow is a row of a flag table: | `--name <value>` | ... or | `-v` | ...
	readmeFlagRow = regexp.MustCompile("(?m)^\\| `-{1,2}([A-Za-z][A-Za-z-]*)")
	// helpFlagLine is a flag as flag.PrintDefaults and the common-flags block print it.
	helpFlagLine = regexp.MustCompile(`(?m)^  -{1,2}([A-Za-z][A-Za-z-]*)`)
	nextHeading  = regexp.MustCompile(`\n#{2,3} `)
)

// readmeSection is the text under a heading, up to the next heading of level 2 or 3.
func readmeSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("bench/README.md has no %q section", heading)
	}
	body := doc[start+len(heading)+2:]
	if loc := nextHeading.FindStringIndex(body); loc != nil {
		body = body[:loc[0]]
	}
	return body
}

// flagNames are the first submatches of re in text, sorted.
func flagNames(re *regexp.Regexp, text string) []string {
	var names []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return names
}

// helpBlock is the lines under header in the help, up to the next blank line.
func helpBlock(t *testing.T, help, header string) []string {
	t.Helper()
	_, rest, ok := strings.Cut(help, header+"\n")
	if !ok {
		t.Fatalf("the help has no %q block:\n%s", header, help)
	}
	block, _, _ := strings.Cut(rest, "\n\n")
	return strings.Split(block, "\n")
}

// oneLine collapses all whitespace, so that the indentation of a synopsis' continuation lines
// does not matter.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestReadmeMatchesHelp(t *testing.T) {
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	help, stderr, code := runArgs(t, "--help")
	if code != exitcode.OK {
		t.Fatalf("--help: exit code %d, stderr %q", code, stderr)
	}

	var helpCommands []string
	for _, line := range helpBlock(t, help, "Commands:") {
		helpCommands = append(helpCommands, strings.Fields(line)[0])
	}
	slices.Sort(helpCommands)

	var declared []string
	for _, c := range commands {
		declared = append(declared, c.name)
	}
	slices.Sort(declared)
	if !slices.Equal(helpCommands, declared) {
		t.Fatalf("--help lists commands %v, the command table has %v", helpCommands, declared)
	}

	var readmeCommands []string
	for _, m := range readmeCommandHeading.FindAllStringSubmatch(doc, -1) {
		readmeCommands = append(readmeCommands, m[1])
	}
	slices.Sort(readmeCommands)
	if !slices.Equal(readmeCommands, helpCommands) {
		t.Errorf("bench/README.md documents commands %v, --help lists %v", readmeCommands, helpCommands)
	}

	common := flagNames(helpFlagLine, strings.Join(helpBlock(t, help, "Common flags, accepted by every command:"), "\n"))
	if got := flagNames(readmeFlagRow, readmeSection(t, doc, "### Common flags")); !slices.Equal(got, common) {
		t.Errorf("bench/README.md lists the common flags %v, --help lists %v", got, common)
	}

	for _, c := range commands {
		t.Run(c.name, func(t *testing.T) {
			out, stderr, code := runArgs(t, c.name, "--help")
			if code != exitcode.OK {
				t.Fatalf("%s --help: exit code %d, stderr %q", c.name, code, stderr)
			}

			var own []string
			for _, name := range flagNames(helpFlagLine, out) {
				if !slices.Contains(common, name) {
					own = append(own, name)
				}
			}
			section := readmeSection(t, doc, "### `orchbench "+c.name+"`")
			if got := flagNames(readmeFlagRow, section); !slices.Equal(got, own) {
				t.Errorf("bench/README.md lists the flags %v of %s, --help lists %v", got, c.name, own)
			}
			if !strings.Contains(oneLine(section), oneLine(c.synopsis)) {
				t.Errorf("bench/README.md does not show the synopsis of %s:\n%s", c.name, c.synopsis)
			}
		})
	}
}
