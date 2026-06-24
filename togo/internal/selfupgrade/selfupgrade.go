// Package selfupgrade replaces the running binary with the latest published
// GitHub release. The release API and asset downloads are public (no token),
// and each platform's binary is uploaded as a raw release asset named
// cappu-<os>-<arch>. Self-contained; fetchers are injectable for tests. Port of
// src/selfupgrade/selfupgrade.ts.
package selfupgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	repoOwner = "nikeee"
	repoName  = "cappu"
	githubAPI = "https://api.github.com"
)

// PlatformTarget is the release asset name for a platform, or ok=false when the
// os/arch is unknown. Every linux/darwin/windows x amd64/arm64 combination is
// built (see the Makefile build-all targets).
func PlatformTarget(goos, goarch string) (string, bool) {
	os := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "windows"}[goos]
	cpu := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if os == "" || cpu == "" {
		return "", false
	}
	// Asset names match `make build-all` output (the dist filenames), so CD can
	// upload dist/* with no renames. Windows keeps the .exe so the downloaded
	// asset is runnable as-is.
	if os == "windows" {
		return fmt.Sprintf("cappu-win-%s.exe", cpu), true
	}
	return fmt.Sprintf("cappu-%s-%s", os, cpu), true
}

// FetchJSON GETs a url and returns the body. FetchBytes GETs a (possibly large)
// url, reporting progress.
type (
	FetchJSON        func(url string) ([]byte, error)
	DownloadProgress func(received, total int64)
	FetchBytes       func(url string, onProgress DownloadProgress) ([]byte, error)
)

// ReleaseRef is the release asset to upgrade from, with the release it belongs to.
type ReleaseRef struct {
	AssetName   string
	AssetURL    string
	Tag         string
	PublishedAt string
}

// LatestRelease is the asset matching assetName in the latest published release.
func LatestRelease(assetName string, fetchJSON FetchJSON) (ReleaseRef, error) {
	raw, err := fetchJSON(fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPI, repoOwner, repoName))
	if err != nil {
		return ReleaseRef{}, err
	}
	var release struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return ReleaseRef{}, err
	}
	if release.TagName == "" {
		return ReleaseRef{}, fmt.Errorf("no published release found")
	}
	for _, a := range release.Assets {
		if a.Name == assetName {
			return ReleaseRef{AssetName: a.Name, AssetURL: a.DownloadURL, Tag: release.TagName, PublishedAt: release.PublishedAt}, nil
		}
	}
	return ReleaseRef{}, fmt.Errorf("release %s has no asset '%s'", release.TagName, assetName)
}

// SameVersion reports whether the release tag names the running version. Tags
// are vX.Y.Z; meta.Version is X.Y.Z. ponytail: plain equality, not a semver
// comparison - enough to skip a redundant re-download; swap in a compare if
// downgrade protection is ever needed.
func SameVersion(tag, currentVersion string) bool {
	return currentVersion != "" && strings.TrimPrefix(tag, "v") == currentVersion
}

// DownloadBinary downloads the raw binary release asset.
func DownloadBinary(assetURL string, fetchBytes FetchBytes, onProgress DownloadProgress) ([]byte, error) {
	return fetchBytes(assetURL, onProgress)
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

// Result reports a completed upgrade. UpToDate is true when the running version
// already matched the latest release and nothing was downloaded or replaced.
type Result struct {
	AssetName  string
	Release    ReleaseRef
	TargetPath string
	UpToDate   bool
}

// Options configures an upgrade. Fetchers are injectable; otherwise the public
// GitHub fetchers are used.
type Options struct {
	TargetPath     string
	CurrentVersion string
	GOOS           string
	GOARCH         string
	FetchJSON      FetchJSON
	FetchBytes     FetchBytes
	OnProgress     DownloadProgress
}

// SelfUpgrade replaces the target (the running binary by default) with the
// latest release build for this platform, unless it is already current.
func SelfUpgrade(opts Options) (Result, error) {
	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	assetName, ok := PlatformTarget(goos, goarch)
	if !ok {
		return Result{}, fmt.Errorf("no cappu build for %s/%s", goos, goarch)
	}
	fetchJSON, fetchBytes := opts.FetchJSON, opts.FetchBytes
	if fetchJSON == nil || fetchBytes == nil {
		fetchJSON, fetchBytes = githubFetchers()
	}
	release, err := LatestRelease(assetName, fetchJSON)
	if err != nil {
		return Result{}, err
	}
	targetPath := opts.TargetPath
	if targetPath == "" {
		if exe, err := os.Executable(); err == nil {
			targetPath = exe
		}
	}
	if SameVersion(release.Tag, opts.CurrentVersion) {
		return Result{AssetName: assetName, Release: release, TargetPath: targetPath, UpToDate: true}, nil
	}
	data, err := DownloadBinary(release.AssetURL, fetchBytes, opts.OnProgress)
	if err != nil {
		return Result{}, err
	}
	if err := ReplaceBinary(targetPath, data); err != nil {
		return Result{}, err
	}
	return Result{AssetName: assetName, Release: release, TargetPath: targetPath}, nil
}
