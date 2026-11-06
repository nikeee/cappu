package packages

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FileRepositorySource is a PackageSource over a local maven2 repository
// directory (~/.m2/repository after `mvn install`, a checked-in repo/ dir).
// POMs and jars are read from disk through MavenRepositorySource's fetchers;
// versions come from the version directories (a local repository has no
// maven-metadata.xml for what `mvn install` wrote). It is never wrapped in the
// on-disk metadata cache: a local repository changes under cappu.
type FileRepositorySource struct {
	*MavenRepositorySource
	dir  string
	name SourceName
}

// NewFileRepositorySource builds a source over the repository rooted at dir,
// displayed (and recorded in the lock) as name - the entry as configured, not
// the expanded directory, which differs per machine.
func NewFileRepositorySource(dir, name string) *FileRepositorySource {
	dir = filepath.Clean(dir)
	read := func(u string) ([]byte, bool, error) {
		// MavenRepositorySource joins dir + "/" + maven2 path with forward slashes.
		rel := strings.TrimPrefix(strings.TrimPrefix(u, dir), "/")
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}
	inner := NewMavenRepositorySourceWithFetchers(dir, "",
		func(u string) (string, bool, error) {
			data, found, err := read(u)
			return string(data), found, err
		},
		read)
	return &FileRepositorySource{MavenRepositorySource: inner, dir: dir, name: SourceName(name)}
}

// Name is the configured entry.
func (s *FileRepositorySource) Name() SourceName { return s.name }

// ListVersions lists the version directories of group:artifact that hold a
// POM, oldest first by Maven ordering.
func (s *FileRepositorySource) ListVersions(groupID, artifactID string) ([]string, error) {
	artifactDir := filepath.Join(s.dir, filepath.FromSlash(artifactPath(groupID, artifactID)))
	entries, err := os.ReadDir(artifactDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pom := filepath.Join(artifactDir, e.Name(), artifactID+"-"+e.Name()+".pom")
		if _, err := os.Stat(pom); err == nil {
			versions = append(versions, e.Name())
		}
	}
	slices.SortFunc(versions, CompareVersions)
	return versions, nil
}
