// Package lockfile reads cappu-lock.json and verifies installed jars against
// the SHA-256 sums it pins. Port of the lockfile/verify parts of
// src/install.ts; resolution and install land with the install command.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

// Name is the lockfile's filename, next to cappu.json.
const Name = "cappu-lock.json"

// Sha256 is a hex SHA-256 digest of a jar's bytes (distinct from the md5/sha1
// sidecars publishing emits).
type Sha256 string

// LockedPackage pins one resolved package to the exact bytes installed.
type LockedPackage struct {
	Coordinates coordinates `json:"coordinates"`
	Source      string      `json:"source"`
	Sha256      Sha256      `json:"sha256"`
}

// coordinates is the on-disk JSON shape of packages.Coordinates (the lockfile
// stores plain lowercase keys).
type coordinates struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
}

func (c coordinates) toDomain() packages.Coordinates {
	return packages.NewCoordinates(c.GroupID, c.ArtifactID, c.Version)
}

// Lockfile is the cappu-lock.json document (only version 2 is honored).
type Lockfile struct {
	Version           int             `json:"version"`
	Roots             json.RawMessage `json:"roots"`
	Packages          []LockedPackage `json:"packages"`
	ProcessorPackages []LockedPackage `json:"processorPackages,omitempty"`
	TestPackages      []LockedPackage `json:"testPackages,omitempty"`
}

// lockTarget maps each locked configuration to the lib directory it installs
// into and the field that holds it.
type lockTarget struct {
	dir      string
	packages func(*Lockfile) []LockedPackage
}

var lockTargets = []lockTarget{
	{config.DefaultClassPath, func(l *Lockfile) []LockedPackage { return l.Packages }},
	{config.DefaultProcessorPath, func(l *Lockfile) []LockedPackage { return l.ProcessorPackages }},
	{config.DefaultTestClassPath, func(l *Lockfile) []LockedPackage { return l.TestPackages }},
}

// VerifyResult reports the outcome of checking installed jars against the lock.
type VerifyResult struct {
	// FromLock is false when there is no cappu-lock.json to verify against.
	FromLock bool
	// OK holds coordinate strings whose installed jar matches its locked SHA-256.
	OK []string
	// Modified holds jars present but whose bytes differ from the lock.
	Modified []string
	// Missing holds packages locked but absent from the lib directory.
	Missing []string
}

// read loads and validates the lockfile, or returns nil when absent/corrupt (a
// corrupt lock is ignored, not fatal: install re-resolves).
func read(cfg *config.Config) *Lockfile {
	path := filepath.Join(cfg.BaseDir, Name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lock Lockfile
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil
	}
	if lock.Version != 2 {
		return nil
	}
	return &lock
}

// VerifyInstalled checks the jars currently in the lib directories against the
// SHA-256 sums in cappu-lock.json. Read-only: nothing is downloaded, written or
// removed. Port of verifyInstalled.
func VerifyInstalled(cfg *config.Config) VerifyResult {
	lock := read(cfg)
	if lock == nil {
		return VerifyResult{}
	}
	result := VerifyResult{FromLock: true}
	for _, target := range lockTargets {
		for _, pkg := range target.packages(lock) {
			c := pkg.Coordinates.toDomain()
			id := string(c.String())
			file := filepath.Join(
				cfg.ResolvePath(target.dir),
				string(c.ArtifactID)+"-"+string(c.Version)+".jar",
			)
			digest, err := sha256File(file)
			switch {
			case errors.Is(err, os.ErrNotExist):
				result.Missing = append(result.Missing, id)
			case err != nil:
				// Unreadable for another reason: treat as modified (cannot confirm).
				result.Modified = append(result.Modified, id)
			case digest == pkg.Sha256:
				result.OK = append(result.OK, id)
			default:
				result.Modified = append(result.Modified, id)
			}
		}
	}
	return result
}

func sha256File(path string) (Sha256, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return Sha256(hex.EncodeToString(sum[:])), nil
}
