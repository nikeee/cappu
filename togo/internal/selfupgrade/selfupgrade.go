// Package selfupgrade replaces the running binary with the freshest CD build:
// the latest successful CD.yaml run's uploaded artifact for this platform
// (cappu-<os>-<arch>, a zip wrapping the single binary). The GitHub artifact
// API needs an actions:read token. Self-contained; fetchers are injectable for
// tests. Port of src/selfupgrade/selfupgrade.ts.
package selfupgrade

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nikeee/cappu/internal/httpx"
)

const (
	repoOwner = "nikeee"
	repoName  = "cappu"
	workflow  = "CD.yaml"
	githubAPI = "https://api.github.com"
)

// Target is the CD artifact and the binary name inside it for a platform.
type Target struct {
	Artifact   string
	BinaryName string
}

// PlatformTarget is the CD artifact matching a platform, or ok=false when none
// is built (windows is x64-only, macOS arm64-only).
func PlatformTarget(goos, goarch string) (Target, bool) {
	os := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "windows"}[goos]
	cpu := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if os == "" || cpu == "" {
		return Target{}, false
	}
	if os == "windows" && cpu != "x64" {
		return Target{}, false
	}
	if os == "darwin" && cpu != "arm64" {
		return Target{}, false
	}
	binary := "cappu"
	if os == "windows" {
		binary = "cappu.exe"
	}
	return Target{Artifact: fmt.Sprintf("cappu-%s-%s", os, cpu), BinaryName: binary}, true
}

// FetchJSON GETs a url and returns the body. FetchBytes GETs a (possibly large)
// url, reporting progress.
type (
	FetchJSON        func(url string) ([]byte, error)
	DownloadProgress func(received, total int64)
	FetchBytes       func(url string, onProgress DownloadProgress) ([]byte, error)
)

// ArtifactRef is the build artifact to upgrade from, with the run it came from.
type ArtifactRef struct {
	ID           int64
	Name         string
	RunSha       string
	RunCreatedAt string
}

// LatestArtifact is the artifact for target in the latest successful CD run on main.
func LatestArtifact(target Target, fetchJSON FetchJSON) (ArtifactRef, error) {
	raw, err := fetchJSON(fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/runs?branch=main&status=success&event=push&per_page=1", githubAPI, repoOwner, repoName, workflow))
	if err != nil {
		return ArtifactRef{}, err
	}
	var runs struct {
		WorkflowRuns []struct {
			ID        int64  `json:"id"`
			HeadSha   string `json:"head_sha"`
			CreatedAt string `json:"created_at"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		return ArtifactRef{}, err
	}
	if len(runs.WorkflowRuns) == 0 {
		return ArtifactRef{}, fmt.Errorf("no successful CD run found on main")
	}
	run := runs.WorkflowRuns[0]

	raw, err = fetchJSON(fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d/artifacts", githubAPI, repoOwner, repoName, run.ID))
	if err != nil {
		return ArtifactRef{}, err
	}
	var artifacts struct {
		Artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		return ArtifactRef{}, err
	}
	for _, a := range artifacts.Artifacts {
		if a.Name != target.Artifact {
			continue
		}
		if a.Expired {
			return ArtifactRef{}, fmt.Errorf("artifact '%s' from CD run %d has expired", target.Artifact, run.ID)
		}
		return ArtifactRef{ID: a.ID, Name: a.Name, RunSha: run.HeadSha, RunCreatedAt: run.CreatedAt}, nil
	}
	return ArtifactRef{}, fmt.Errorf("CD run %d has no artifact '%s'", run.ID, target.Artifact)
}

// DownloadBinary downloads the artifact zip and extracts the single binary.
func DownloadBinary(artifactID int64, binaryName string, fetchBytes FetchBytes, onProgress DownloadProgress) ([]byte, error) {
	zipBytes, err := fetchBytes(fmt.Sprintf("%s/repos/%s/%s/actions/artifacts/%d/zip", githubAPI, repoOwner, repoName, artifactID), onProgress)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("the downloaded artifact is not a valid zip")
	}
	var entry *zip.File
	for _, f := range zr.File {
		if f.Name == binaryName {
			entry = f
			break
		}
	}
	if entry == nil {
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, "/") {
				entry = f
				break
			}
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("the artifact did not contain %s", binaryName)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return httpx.ReadAllCapped(rc)
}

// ReplaceBinary replaces targetPath with bytes, executable. POSIX renames over
// the running binary (the process keeps the old inode); Windows moves the old
// one aside first and restores it on failure.
func ReplaceBinary(targetPath string, data []byte) error {
	staged := filepath.Join(filepath.Dir(targetPath), fmt.Sprintf(".%s.upgrade-%d", filepath.Base(targetPath), os.Getpid()))
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := fmt.Sprintf("%s.old-%d", targetPath, os.Getpid())
		if err := os.Rename(targetPath, old); err != nil {
			return err
		}
		if err := os.Rename(staged, targetPath); err != nil {
			_ = os.Rename(old, targetPath) // put the working binary back
			return err
		}
		_ = os.Remove(old) // best effort
		return nil
	}
	return os.Rename(staged, targetPath)
}

// ResolveToken returns a GitHub token from the environment, else `gh auth
// token`. ok is false when none is available.
func ResolveToken() (string, bool) {
	for _, name := range []string{"CAPPU_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v, true
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return token, true
		}
	}
	return "", false
}

// Result reports a completed upgrade.
type Result struct {
	Target     Target
	Artifact   ArtifactRef
	TargetPath string
}

// Options configures an upgrade. Fetchers are injectable; otherwise Token
// builds authenticated ones.
type Options struct {
	TargetPath string
	Token      string
	GOOS       string
	GOARCH     string
	FetchJSON  FetchJSON
	FetchBytes FetchBytes
	OnProgress DownloadProgress
}

// SelfUpgrade replaces the target (the running binary by default) with the
// latest CD build for this platform.
func SelfUpgrade(opts Options) (Result, error) {
	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	target, ok := PlatformTarget(goos, goarch)
	if !ok {
		return Result{}, fmt.Errorf("no cappu build for %s/%s", goos, goarch)
	}
	fetchJSON, fetchBytes := opts.FetchJSON, opts.FetchBytes
	if fetchJSON == nil || fetchBytes == nil {
		if opts.Token == "" {
			return Result{}, fmt.Errorf("a GitHub token is required (set GITHUB_TOKEN)")
		}
		fetchJSON, fetchBytes = githubFetchers(opts.Token)
	}
	artifact, err := LatestArtifact(target, fetchJSON)
	if err != nil {
		return Result{}, err
	}
	data, err := DownloadBinary(artifact.ID, target.BinaryName, fetchBytes, opts.OnProgress)
	if err != nil {
		return Result{}, err
	}
	targetPath := opts.TargetPath
	if targetPath == "" {
		if exe, err := os.Executable(); err == nil {
			targetPath = exe
		}
	}
	if err := ReplaceBinary(targetPath, data); err != nil {
		return Result{}, err
	}
	return Result{Target: target, Artifact: artifact, TargetPath: targetPath}, nil
}
