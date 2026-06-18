// Package lockfile reads and writes cappu-lock.json and verifies installed jars
// against the SHA-256 sums it pins. Port of the lockfile parts of
// src/install.ts (the install orchestration lives in internal/install).
//
// The on-disk types carry easyjson-generated, reflection-free marshalers
// (lockfile_easyjson.go); regenerate with `go generate ./...` after changing
// any of the //easyjson:json-tagged types below.
package lockfile

//go:generate easyjson lockfile.go

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
//
//easyjson:json
type LockedPackage struct {
	Coordinates coordinates        `json:"coordinates"`
	Source      string             `json:"source"`
	Sha256      Sha256             `json:"sha256"`
	Licenses    []packages.License `json:"licenses,omitempty"`
}

// NewLockedPackage builds a locked package from domain coordinates.
func NewLockedPackage(c packages.Coordinates, source string, sha Sha256, licenses []packages.License) LockedPackage {
	return LockedPackage{Coordinates: newCoordinates(c), Source: source, Sha256: sha, Licenses: licenses}
}

// Coords returns the package's domain coordinates.
func (p LockedPackage) Coords() packages.Coordinates { return p.Coordinates.toDomain() }

// coordinates is the on-disk JSON shape of packages.Coordinates (the lockfile
// stores plain lowercase keys).
//
//easyjson:json
type coordinates struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
}

func newCoordinates(c packages.Coordinates) coordinates {
	return coordinates{GroupID: string(c.GroupID), ArtifactID: string(c.ArtifactID), Version: string(c.Version)}
}

func (c coordinates) toDomain() packages.Coordinates {
	return packages.NewCoordinates(c.GroupID, c.ArtifactID, c.Version)
}

// Lockfile is the cappu-lock.json document (only version 2 is honored).
//
//easyjson:json
type Lockfile struct {
	Version int `json:"version"`
	// Roots is the dependencies section this lock was resolved from.
	Roots             config.Dependencies `json:"roots"`
	Packages          []LockedPackage     `json:"packages"`
	ProcessorPackages []LockedPackage     `json:"processorPackages,omitempty"`
	TestPackages      []LockedPackage     `json:"testPackages,omitempty"`
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

// Path is the lockfile location next to cappu.json.
func Path(cfg *config.Config) string {
	return filepath.Join(cfg.BaseDir, Name)
}

// Read loads and validates the lockfile, or returns nil when absent/corrupt (a
// corrupt lock is ignored, not fatal: install re-resolves).
func Read(cfg *config.Config) *Lockfile {
	raw, err := os.ReadFile(Path(cfg))
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

// Write serializes the lockfile next to cappu.json (2-space indent, trailing
// newline - matching the Node build).
func Write(cfg *config.Config, lock *Lockfile) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(cfg), append(data, '\n'), 0o644)
}

// Matches reports whether the lock was resolved from exactly this dependencies
// section. Empty configurations are dropped before comparing, so locks written
// before a configuration existed do not turn stale when the schema grows.
func (l *Lockfile) Matches(cfg *config.Config) bool {
	return normalizeDeps(l.Roots) == normalizeDeps(cfg.Dependencies)
}

func normalizeDeps(d config.Dependencies) string {
	m := map[string]map[string]string{}
	for name, entries := range map[string]map[string]string{
		"api":                 d.API,
		"implementation":      d.Implementation,
		"annotationProcessor": d.AnnotationProcessor,
		"testImplementation":  d.TestImplementation,
	} {
		if len(entries) > 0 {
			m[name] = entries
		}
	}
	b, _ := json.Marshal(m) // json.Marshal sorts map keys: deterministic
	return string(b)
}

// VerifyInstalled checks the jars currently in the lib directories against the
// SHA-256 sums in cappu-lock.json. Read-only: nothing is downloaded, written or
// removed. Port of verifyInstalled.
func VerifyInstalled(cfg *config.Config) VerifyResult {
	lock := Read(cfg)
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
	return Sha256Of(bytes), nil
}

// Sha256Of is the hex SHA-256 of bytes.
func Sha256Of(bytes []byte) Sha256 {
	sum := sha256.Sum256(bytes)
	return Sha256(hex.EncodeToString(sum[:]))
}
