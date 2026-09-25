package projectctx

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const maxDependencies = 30

type metadata struct {
	language     string
	projectType  string
	module       string
	dependencies []string
}

// manifest is a dependency file that identifies the project's ecosystem. Checked in
// order at the repository root; the first present one wins.
type manifest struct {
	file        string
	language    string
	projectType string
	parse       func(content string) (module string, deps []string)
}

var manifests = []manifest{
	{"go.mod", "go", "go-module", parseGoMod},
	{"package.json", "javascript", "npm-package", parsePackageJSON},
	{"pyproject.toml", "python", "python-project", parsePyproject},
	{"Cargo.toml", "rust", "cargo-crate", parseCargo},
	{"pom.xml", "java", "maven-project", parsePom},
}

// languageByExt is the fallback when no manifest is present: the most common source
// extension decides.
var languageByExt = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".jsx": "javascript", ".ts": "typescript",
	".tsx": "typescript", ".rs": "rust", ".java": "java", ".kt": "kotlin", ".rb": "ruby",
	".php": "php", ".cs": "csharp", ".c": "c", ".h": "c", ".cpp": "cpp", ".swift": "swift",
}

func projectMetadata(root string, files []string) metadata {
	rootFiles := map[string]bool{}
	for _, f := range files {
		if !strings.Contains(f, "/") {
			rootFiles[f] = true
		}
	}
	for _, m := range manifests {
		if !rootFiles[m.file] {
			continue
		}
		md := metadata{language: m.language, projectType: m.projectType}
		if content, ok := readText(root, m.file); ok {
			md.module, md.dependencies = m.parse(content)
		}
		if m.file == "package.json" && rootFiles["tsconfig.json"] {
			md.language = "typescript"
		}
		if len(md.dependencies) > maxDependencies {
			md.dependencies = md.dependencies[:maxDependencies]
		}
		return md
	}
	return metadata{language: dominantLanguage(files)}
}

func dominantLanguage(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		if lang, ok := languageByExt[strings.ToLower(extOf(f))]; ok {
			counts[lang]++
		}
	}
	best, bestN := "", 0
	for _, lang := range sortedKeysInt(counts) {
		if counts[lang] > bestN {
			best, bestN = lang, counts[lang]
		}
	}
	return best
}

var goModuleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// parseGoMod returns the module path and the direct requirements (no // indirect).
func parseGoMod(content string) (string, []string) {
	module := ""
	if m := goModuleRe.FindStringSubmatch(content); m != nil {
		module = m[1]
	}
	var deps []string
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !inBlock:
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") || strings.Contains(line, "// indirect") {
			continue
		}
		deps = append(deps, strings.Fields(line)[0])
	}
	return module, deps
}

func parsePackageJSON(content string) (string, []string) {
	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(content), &pkg); err != nil {
		return "", nil
	}
	deps := sortedKeysStr(pkg.Dependencies)
	return pkg.Name, append(deps, sortedKeysStr(pkg.DevDependencies)...)
}

var (
	tomlSectionRe = regexp.MustCompile(`^\[([^\]]+)\]\s*$`)
	tomlNameRe    = regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	tomlKeyRe     = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*=`)
	quotedRe      = regexp.MustCompile(`"([^"]+)"`)
	pyReqNameRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+`)
)

// parsePyproject reads [project] name and dependencies. It is a line-based reading of
// the common layout, not a TOML parser: metadata is a hint for classification, and an
// unusual layout just yields fewer dependencies.
func parsePyproject(content string) (string, []string) {
	name, section := "", ""
	var deps []string
	inDeps := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if m := tomlSectionRe.FindStringSubmatch(line); m != nil {
			section, inDeps = m[1], false
			continue
		}
		if section != "project" {
			continue
		}
		if m := tomlNameRe.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		if strings.HasPrefix(line, "dependencies") && strings.Contains(line, "[") {
			inDeps = !strings.Contains(line, "]")
			line = line[strings.Index(line, "[")+1:]
		} else if !inDeps {
			continue
		}
		if strings.Contains(line, "]") {
			inDeps = false
		}
		for _, q := range quotedRe.FindAllStringSubmatch(line, -1) {
			if n := pyReqNameRe.FindString(q[1]); n != "" {
				deps = append(deps, n)
			}
		}
	}
	return name, deps
}

func parseCargo(content string) (string, []string) {
	name, section := "", ""
	var deps []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if m := tomlSectionRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		switch section {
		case "package":
			if m := tomlNameRe.FindStringSubmatch(line); m != nil {
				name = m[1]
			}
		case "dependencies":
			if m := tomlKeyRe.FindStringSubmatch(line); m != nil {
				deps = append(deps, m[1])
			}
		}
	}
	return name, deps
}

var (
	pomDependencyRe = regexp.MustCompile(`(?s)<dependency>.*?</dependency>`)
	pomArtifactRe   = regexp.MustCompile(`<artifactId>\s*([^<\s]+)\s*</artifactId>`)
	pomDepsBlockRe  = regexp.MustCompile(`(?s)<dependencies>.*</dependencies>`)
	pomParentRe     = regexp.MustCompile(`(?s)<parent>.*?</parent>`)
)

func parsePom(content string) (string, []string) {
	head := pomParentRe.ReplaceAllString(pomDepsBlockRe.ReplaceAllString(content, ""), "")
	name := ""
	if m := pomArtifactRe.FindStringSubmatch(head); m != nil {
		name = m[1]
	}
	var deps []string
	for _, dep := range pomDependencyRe.FindAllString(content, -1) {
		if m := pomArtifactRe.FindStringSubmatch(dep); m != nil {
			deps = append(deps, m[1])
		}
	}
	return name, deps
}

func sortedKeysStr(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysInt(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
