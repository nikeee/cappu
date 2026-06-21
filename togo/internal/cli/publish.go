package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/publish"
)

// RunPublish handles `cappu publish`: build the project jar, generate its POM,
// and upload both (with checksums) to a Maven registry. Coordinates and a
// registry url are required; credentials come from the environment. Port of
// src/cli/publish.ts (the jar is built via the javac-delegation path).
func RunPublish(cfg *config.Config, repoFlag string) int {
	errp := painter(os.Stderr)
	out := painter(os.Stdout)

	if missing := publish.MissingCoordinates(cfg); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s cappu publish needs %s in cappu.json\n", errp("red", "error:"), strings.Join(missing, ", "))
		return 2
	}
	repo := publish.ResolvePublishRegistry(repoFlag, cfg.PublishRepository, os.Getenv("CAPPU_PUBLISH_REGISTRY"))
	fmt.Fprint(os.Stderr, errp("dim", "publishing to "+repo+"\n"))
	auth, ok := publish.ResolvePublishAuth(os.Getenv("CAPPU_PUBLISH_USERNAME"), os.Getenv("CAPPU_PUBLISH_PASSWORD"), os.Getenv("CAPPU_PUBLISH_TOKEN"))
	if !ok {
		fmt.Fprintf(os.Stderr, "%s no credentials: set CAPPU_PUBLISH_USERNAME + CAPPU_PUBLISH_PASSWORD, or CAPPU_PUBLISH_TOKEN\n", errp("red", "error:"))
		return 2
	}

	// Build the jar (default javac path) over the configured sources.
	inputs := build.SourceJavaFiles(cfg)
	if len(inputs) == 0 {
		fmt.Fprintf(os.Stderr, "%s no sources to compile (configured sourcePaths are empty)\n", errp("red", "error:"))
		return 2
	}
	result := compile.RunCompile(inputs, compile.Options{Output: "jar", Config: cfg})
	for _, w := range result.Warnings {
		fmt.Fprint(os.Stderr, errp("yellow", "warning: "+w+"\n"))
	}
	if !result.Success {
		renderDiagnostics(result.Diagnostics)
		return 1
	}
	jarPath := ""
	for _, f := range result.Written {
		if strings.HasSuffix(f, ".jar") {
			jarPath = f
			break
		}
	}
	jarBytes, err := os.ReadFile(jarPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}
	pom, err := publish.GeneratePom(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}

	base := cfg.ArtifactBaseName()
	coordinates := packages.NewCoordinates(cfg.GroupID, cfg.ArtifactID, cfg.Version)
	uploaded, err := publish.PublishArtifacts(publish.Options{
		Repo:        repo,
		Coordinates: coordinates,
		Files: []publish.File{
			{Filename: base + ".jar", Bytes: jarBytes},
			{Filename: base + ".pom", Bytes: []byte(pom)},
		},
		Auth:     &auth,
		OnUpload: func(url string) { fmt.Fprint(os.Stderr, errp("dim", "uploading "+url+"\n")) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s publish failed: %s\n", errp("red", "error:"), err)
		return 1
	}
	id := string(coordinates.String())
	fmt.Fprintf(os.Stdout, "%s published %s (%d files) to %s\n", out("green", "✓"), out("bold", id), len(uploaded), repo)
	return 0
}
