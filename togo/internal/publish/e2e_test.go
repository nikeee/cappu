package publish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/packages"
)

// End-to-end: publish a tiny library to a throwaway Reposilite registry, then
// install it back from that registry. Mirrors src/publish/publish.e2e.test.ts.
// Gated on Docker AND javac (publish builds the jar with javac); skipped when
// either is missing.
func TestPublishInstallRoundTrip(t *testing.T) {
	if !hasJavac() {
		t.Skip("javac not on PATH")
	}
	if !hasDocker() {
		t.Skip("docker not available")
	}

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "dzikoysk/reposilite:3.5.22",
			ExposedPorts: []string{"8080/tcp"},
			// The entrypoint forwards $REPOSILITE_OPTS; --token mints an
			// all-permissions token, releases is public-read.
			Env:        map[string]string{"REPOSILITE_OPTS": "--token deployer:secret"},
			WaitingFor: wait.ForHTTP("/").WithPort("8080/tcp").WithStartupTimeout(180 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("could not start reposilite (no usable docker?): %v", err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatal(err)
	}
	repo := "http://" + host + ":" + port.Port() + "/releases"

	// 1. Build + publish a tiny library.
	libDir := t.TempDir()
	writeProject(t, libDir, map[string]string{
		"cappu.json": `{"groupId":"com.example","artifactId":"lib","version":"1.0.0"}`,
		filepath.Join("src", "main", "java", "com", "example", "Hello.java"): "package com.example;\npublic class Hello { public static String greet() { return \"hi\"; } }\n",
	})
	libCfg, err := config.Load("", libDir)
	if err != nil {
		t.Fatal(err)
	}
	jarPath, err := build.BuildJar(libCfg)
	if err != nil {
		t.Fatalf("build jar: %v", err)
	}
	jarBytes, err := os.ReadFile(jarPath)
	if err != nil {
		t.Fatal(err)
	}
	pom, err := GeneratePom(libCfg)
	if err != nil {
		t.Fatal(err)
	}
	auth := Auth{Basic: true, Username: "deployer", Password: "secret"}
	if _, err := PublishArtifacts(Options{
		Repo:        repo,
		Coordinates: packages.NewCoordinates("com.example", "lib", "1.0.0"),
		Files: []File{
			{Filename: "lib-1.0.0.jar", Bytes: jarBytes},
			{Filename: "lib-1.0.0.pom", Bytes: []byte(pom)},
		},
		Auth: &auth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// 2. Install it back from the registry into a fresh consumer project.
	consumer := t.TempDir()
	t.Setenv("CAPPU_PACKAGE_STORE", filepath.Join(t.TempDir(), "store"))
	writeProject(t, consumer, map[string]string{
		"cappu.json": `{"packageSources":["` + repo + `"],"dependencies":{"implementation":{"com.example:lib":"1.0.0"}}}`,
	})
	consumerCfg, err := config.Load("", consumer)
	if err != nil {
		t.Fatal(err)
	}
	res, err := install.Dependencies(consumerCfg, nil, install.Options{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.Resolution.Missing) != 0 {
		t.Fatalf("install reported missing: %v", res.Resolution.Missing)
	}
	installed := filepath.Join(consumerCfg.ResolvePath(config.DefaultClassPath), "lib-1.0.0.jar")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("published jar did not install back: %v", err)
	}
}

func writeProject(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hasJavac() bool {
	return exec.Command("javac", "-version").Run() == nil
}

func hasDocker() bool {
	return exec.Command("docker", "info").Run() == nil
}
