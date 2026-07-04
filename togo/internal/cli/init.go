package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

const gitignoreTemplate = `# installed dependencies, provisioned JDKs, generated sources, local state
/.cappu/

# build output of ` + "`cappu compile`" + `
/dist/
`

// initAnswers are the project coordinates + build output `cappu init` asks for.
type initAnswers struct {
	GroupID    string
	ArtifactID string
	Version    string
	Output     string
}

var nonMavenID = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// sanitizeID reduces a directory name to a valid Maven artifactId.
func sanitizeID(name string) string {
	cleaned := strings.Trim(nonMavenID.ReplaceAllString(name, "-"), "-")
	if cleaned == "" {
		return "app"
	}
	return cleaned
}

func initDefaults(projectDir string) initAnswers {
	return initAnswers{
		GroupID:    "com.example",
		ArtifactID: sanitizeID(filepath.Base(projectDir)),
		Version:    "1.0.0",
		Output:     "fat-jar",
	}
}

// initConfigJSON is the cappu.json `cappu init` writes (key order matters).
type initConfigJSON struct {
	Schema          string `json:"$schema"`
	GroupID         string `json:"groupId"`
	ArtifactID      string `json:"artifactId"`
	Version         string `json:"version"`
	CompilerOptions struct {
		Output string `json:"output"`
	} `json:"compilerOptions"`
	Dependencies struct {
		API                 map[string]string `json:"api"`
		Implementation      map[string]string `json:"implementation"`
		AnnotationProcessor map[string]string `json:"annotationProcessor"`
		TestImplementation  map[string]string `json:"testImplementation"`
	} `json:"dependencies"`
}

// renderInitConfig is the cappu.json contents for the chosen answers.
func renderInitConfig(a initAnswers) string {
	var c initConfigJSON
	c.Schema = "./" + config.SchemaFileName
	c.GroupID, c.ArtifactID, c.Version = a.GroupID, a.ArtifactID, a.Version
	c.CompilerOptions.Output = a.Output
	c.Dependencies.API = map[string]string{}
	c.Dependencies.Implementation = map[string]string{}
	c.Dependencies.AnnotationProcessor = map[string]string{}
	c.Dependencies.TestImplementation = map[string]string{}
	data, _ := json.MarshalIndent(c, "", "  ")
	return string(data) + "\n"
}

// ask prompts for each field (stdin, defaulting to base). A minimal line-based
// prompt rather than the Node build's inquirer UI - same data, no TUI dep.
func ask(base initAnswers) initAnswers {
	r := bufio.NewReader(os.Stdin)
	prompt := func(label, def string, valid *regexp.Regexp, hint string) string {
		for {
			fmt.Fprintf(os.Stderr, "%s (%s): ", label, def)
			line, _ := r.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				line = def
			}
			if valid == nil || valid.MatchString(line) {
				return line
			}
			fmt.Fprintf(os.Stderr, "  %s\n", hint)
		}
	}
	a := initAnswers{
		GroupID:    prompt("groupId", base.GroupID, config.MavenID, "letters, digits, '.', '_' or '-' only"),
		ArtifactID: prompt("artifactId", base.ArtifactID, config.MavenID, "letters, digits, '.', '_' or '-' only"),
		Version:    prompt("version", base.Version, config.Semver, "must be semver, e.g. 1.0.0"),
	}
	// The same reader: a second bufio.Reader would lose lines the first one
	// already buffered (piped stdin arrives in one chunk).
	a.Output = promptOutput(r)
	return a
}

// promptOutput asks for the build output as a numbered choice (default fat-jar).
func promptOutput(r *bufio.Reader) string {
	options := []struct{ label, value string }{
		{"application (fat-jar)", "fat-jar"},
		{"library (jar)", "jar"},
		{"classes", "classes"},
	}
	for {
		fmt.Fprintln(os.Stderr, "build output:")
		for i, o := range options {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, o.label)
		}
		fmt.Fprint(os.Stderr, "choice (1): ")
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return "fat-jar"
		}
		switch line {
		case "1":
			return "fat-jar"
		case "2":
			return "jar"
		case "3":
			return "classes"
		}
	}
}

// RunInit scaffolds a project: write cappu.json (asking for coordinates and
// build output, or taking defaults with -y), create the default directories and
// a .gitignore; --with-schema also writes the JSON schema. Port of src/cli/init.ts.
func RunInit(configPath string, withSchema, yes bool) int {
	target := config.DefaultConfigName
	if configPath != "" {
		target = configPath
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	target = abs
	projectDir := filepath.Dir(target)

	answers := initDefaults(projectDir)
	if !yes {
		answers = ask(answers)
	}

	// O_EXCL: create only if absent (no exists/write race).
	if err := writeNew(target, []byte(renderInitConfig(answers))); err != nil {
		if os.IsExist(err) {
			fmt.Fprintf(os.Stderr, "cappu: %s already exists, not overwriting\n", target)
			return 1
		}
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}

	for _, dir := range []string{
		config.DefaultClassPath, config.DefaultTestClassPath,
		config.DefaultSourcePath, config.DefaultResourcePath,
		config.DefaultTestSourcePath, config.DefaultTestResourcePath,
	} {
		if err := os.MkdirAll(filepath.Join(projectDir, dir), 0o755); err != nil {
			// A failed layout dir is a warning, not a failure: cappu.json is
			// already written and usable. Same as the TS build.
			fmt.Fprintf(os.Stderr, "warning: could not create %s: %s\n", dir, err)
		}
	}

	if err := writeNew(filepath.Join(projectDir, ".gitignore"), []byte(gitignoreTemplate)); err != nil {
		if os.IsExist(err) {
			fmt.Fprintln(os.Stderr, ".gitignore already exists, left unchanged - add /.cappu/ and /dist/ if missing")
		} else {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
	}
	fmt.Fprintln(os.Stdout, target)

	if withSchema {
		schema, err := config.JSONSchema()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		schemaTarget := filepath.Join(projectDir, config.SchemaFileName)
		if err := os.WriteFile(schemaTarget, []byte(schema), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, schemaTarget)
	}
	return 0
}

// writeNew creates a file only if it does not already exist (O_EXCL).
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
