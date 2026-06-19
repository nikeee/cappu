package services

import (
	"strings"
	"testing"
)

// Port of src/services/dependencyLens.test.ts.

const depConfig = `{
  // Project configuration.
  "$schema": "./cappu.schema.json",
  "compilerOptions": {
    "outDir": "./build",
  },
  "packageSources": ["https://repo.maven.apache.org/maven2"],
  "dependencies": {
    "api": {
      "org.lib:core": "1.0",
    },
    "implementation": {
      // "org.commented:out": "9.9",
      "com.google.code.gson:gson": "2.10.1",
    },
  },
}
`

func TestFindDependencyEntries(t *testing.T) {
	entries := FindDependencyEntries(depConfig)
	var coords []string
	for _, e := range entries {
		coords = append(coords, e.GroupID+":"+e.ArtifactID+"@"+e.Version)
	}
	want := []string{"org.lib:core@1.0", "com.google.code.gson:gson@2.10.1"}
	if strings.Join(coords, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", coords, want)
	}
	gson := entries[1]
	if gson.Line != 13 {
		t.Errorf("gson line = %d, want 13", gson.Line)
	}
	lineText := strings.Split(depConfig, "\n")[gson.Line]
	if got := lineText[gson.StartChar:gson.EndChar]; got != `"com.google.code.gson:gson": "2.10.1"` {
		t.Errorf("span = %q", got)
	}
}

func TestDependencyLensNewerVersion(t *testing.T) {
	lookup := func(groupID, artifactID string) (string, bool) {
		if groupID+":"+artifactID == "com.google.code.gson:gson" {
			return "2.14.0", true
		}
		return "", false
	}
	lenses := DependencyLenses(depConfig, lookup)
	if len(lenses) != 1 || lenses[0].Title != "newer version: 2.14.0" || lenses[0].Entry.ArtifactID != "gson" {
		t.Errorf("lenses = %+v", lenses)
	}
}

func TestDependencyLensUpToDate(t *testing.T) {
	lenses := DependencyLenses(depConfig, func(_, _ string) (string, bool) { return "1.0", true })
	var arts []string
	for _, l := range lenses {
		arts = append(arts, l.Entry.ArtifactID)
	}
	if len(arts) != 1 || arts[0] != "gson" {
		t.Errorf("artifacts = %v, want [gson]", arts)
	}
}
