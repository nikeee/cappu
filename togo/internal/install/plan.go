package install

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// Version selection for `cappu add` and `cappu update`. Port of pickAddVersion
// and planUpdates in src/install.ts.

const (
	pickAttempts   = 5
	updateAttempts = 5
)

// PickedVersion is the version `cappu add` chose for a key.
type PickedVersion struct {
	Version string
	// Compatible is false when no candidate resolved conflict-free and the
	// newest match was returned anyway.
	Compatible bool
}

// PickAddVersion chooses the version for key given an absent or partial spec:
// the newest published version matching spec whose transitive resolution -
// together with everything already configured - is conflict-free. When no
// candidate is clean, the newest match is returned flagged incompatible. ok is
// false when nothing matches at all.
func PickAddVersion(cfg *config.Config, key, spec string, srcs []packages.PackageSource) (PickedVersion, bool, error) {
	groupID, artifactID, _ := strings.Cut(key, ":")
	published, err := listVersions(groupID, artifactID, srcs)
	if err != nil {
		return PickedVersion{}, false, err
	}
	candidates := packages.MatchingVersions(published, spec)
	if len(candidates) == 0 {
		return PickedVersion{}, false, nil
	}

	// The new dependency joins the existing roots (any previous entry for the
	// same key is superseded by the candidate).
	var existing []packages.Coordinates
	for _, c := range sources.CompileRoots(cfg) {
		if string(c.GroupID)+":"+string(c.ArtifactID) != key {
			existing = append(existing, c)
		}
	}
	for _, version := range candidates[:min(pickAttempts, len(candidates))] {
		roots := append(append([]packages.Coordinates(nil), existing...),
			packages.NewCoordinates(groupID, artifactID, version))
		res, err := packages.ResolveTransitive(roots, srcs, nil)
		if err != nil {
			return PickedVersion{}, false, err
		}
		if len(res.Conflicts) == 0 && len(res.Missing) == 0 {
			return PickedVersion{Version: version, Compatible: true}, true, nil
		}
	}
	return PickedVersion{Version: candidates[0], Compatible: false}, true, nil
}

// DependencyBump is one dependency move planned by `cappu update`.
type DependencyBump struct {
	Configuration string
	Key           string
	From          string
	To            string
}

// updateConfigs are the dependency configurations `cappu update` walks.
var updateConfigs = config.Configurations

// prerelease matches qualifiers `cappu update` never auto-bumps to.
var prerelease = regexp.MustCompile(`(?i)(?:^|[-._])(?:alpha|beta|rc|cr|snapshot|milestone|m\d+|preview|ea|dev)`)

func isStableVersion(version string) bool { return !prerelease.MatchString(version) }

// majorOf is the leading (major) component, for the no-major-bump rule.
func majorOf(version string) string {
	return strings.FieldsFunc(version, func(r rune) bool { return r == '.' || r == '+' || r == '-' })[0]
}

// PlanUpdates returns the newest STABLE version each declared dependency can
// move to WITHIN its current major (no major bumps) while keeping its
// configuration's transitive graph conflict-free and complete. Port of
// planUpdates.
func PlanUpdates(cfg *config.Config, srcs []packages.PackageSource) ([]DependencyBump, error) {
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	working := map[string]map[string]string{
		"api":                 clone(cfg.Dependencies.API),
		"implementation":      clone(cfg.Dependencies.Implementation),
		"annotationProcessor": clone(cfg.Dependencies.AnnotationProcessor),
		"testImplementation":  clone(cfg.Dependencies.TestImplementation),
	}
	graphRoots := func(configuration string) []packages.Coordinates {
		switch configuration {
		case "annotationProcessor":
			return sources.RootsOf(working["annotationProcessor"])
		case "testImplementation":
			return sources.RootsOf(working["testImplementation"])
		default:
			return append(sources.RootsOf(working["api"]), sources.RootsOf(working["implementation"])...)
		}
	}

	var bumps []DependencyBump
	for _, configuration := range updateConfigs {
		for _, key := range sortedKeys(working[configuration]) {
			current := working[configuration][key]
			groupID, artifactID, _ := strings.Cut(key, ":")
			published, err := listVersions(groupID, artifactID, srcs)
			if err != nil {
				return nil, err
			}
			order := packages.MatchingVersions(published, "") // newest (publish order) first
			currentIndex := indexOf(order, current)
			if currentIndex < 0 {
				continue // unknown ordering: do not risk a downgrade
			}
			// newer, stable, and the same major (a 2.x dep never auto-bumps to 3.x)
			var newer []string
			for _, v := range order[:currentIndex] {
				if isStableVersion(v) && majorOf(v) == majorOf(current) {
					newer = append(newer, v)
				}
			}
			for _, candidate := range newer[:min(updateAttempts, len(newer))] {
				roots := withVersion(graphRoots(configuration), key, candidate)
				res, err := packages.ResolveTransitive(roots, srcs, nil)
				if err != nil {
					return nil, err
				}
				if len(res.Conflicts) == 0 && len(res.Missing) == 0 {
					working[configuration][key] = candidate
					bumps = append(bumps, DependencyBump{Configuration: configuration, Key: key, From: current, To: candidate})
					break
				}
			}
		}
	}
	return bumps, nil
}

// OutdatedDependency is a declared dependency with a newer published version.
type OutdatedDependency struct {
	Configuration string
	Key           string
	Current       string
	Wanted        string // newest stable within the current major (safe update), "" if none
	Latest        string // newest stable overall (a major bump), "" if none
}

// PlanOutdated returns every declared dependency that has a newer published
// stable version, with the newest in-major version (Wanted) and newest overall
// (Latest). Read-only. Port of planOutdated.
func PlanOutdated(cfg *config.Config, srcs []packages.PackageSource) ([]OutdatedDependency, error) {
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	sections := []struct {
		configuration string
		deps          map[string]string
	}{
		{"api", cfg.Dependencies.API},
		{"implementation", cfg.Dependencies.Implementation},
		{"annotationProcessor", cfg.Dependencies.AnnotationProcessor},
		{"testImplementation", cfg.Dependencies.TestImplementation},
	}
	var out []OutdatedDependency
	for _, section := range sections {
		for _, key := range sortedKeys(section.deps) {
			current := section.deps[key]
			groupID, artifactID, _ := strings.Cut(key, ":")
			published, err := listVersions(groupID, artifactID, srcs)
			if err != nil {
				return nil, err
			}
			order := packages.MatchingVersions(published, "") // newest first
			currentIndex := indexOf(order, current)
			if currentIndex < 0 {
				continue // unknown ordering: do not guess
			}
			var newer []string
			for _, v := range order[:currentIndex] {
				if isStableVersion(v) {
					newer = append(newer, v)
				}
			}
			if len(newer) == 0 {
				continue
			}
			row := OutdatedDependency{Configuration: section.configuration, Key: key, Current: current}
			if latest := newer[0]; latest != current {
				row.Latest = latest
			}
			for _, v := range newer {
				if majorOf(v) == majorOf(current) {
					if v != current {
						row.Wanted = v
					}
					break
				}
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// listVersions returns the published versions from the first source that knows
// the package.
func listVersions(groupID, artifactID string, srcs []packages.PackageSource) ([]string, error) {
	for _, source := range srcs {
		versions, err := source.ListVersions(groupID, artifactID)
		if err != nil {
			return nil, err
		}
		if len(versions) > 0 {
			return versions, nil
		}
	}
	return nil, nil
}

// withVersion returns roots with the entry for key set to version.
func withVersion(roots []packages.Coordinates, key, version string) []packages.Coordinates {
	out := make([]packages.Coordinates, len(roots))
	for i, c := range roots {
		if string(c.GroupID)+":"+string(c.ArtifactID) == key {
			out[i] = packages.NewCoordinates(string(c.GroupID), string(c.ArtifactID), version)
		} else {
			out[i] = c
		}
	}
	return out
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
