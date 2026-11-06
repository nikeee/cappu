package install

import (
	"errors"
	"maps"
	"regexp"
	"slices"
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
// together with everything already configured, under the platform-managed
// versions - is conflict-free. When no candidate is clean, the newest match is
// returned flagged incompatible. ok is false when nothing matches at all.
func PickAddVersion(cfg *config.Config, key, spec string, srcs []packages.PackageSource, managed map[packages.PackageKey]string) (PickedVersion, bool, error) {
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
	var existing []packages.DependencyDeclaration
	for _, c := range sources.MainRoots(cfg) {
		if string(c.Coordinates.GAV().Key()) != key {
			existing = append(existing, c)
		}
	}
	for _, version := range candidates[:min(pickAttempts, len(candidates))] {
		roots := append(append([]packages.DependencyDeclaration(nil), existing...),
			packages.DependencyDeclaration{Coordinates: packages.NewCoordinates(groupID, artifactID, version)})
		res, err := packages.ResolveTransitive(roots, srcs, packages.ResolveOptions{Managed: managed})
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
// Prefix-up-to-separator like the TS split (so "-beta" or "." yields ""),
// never a panic on separator-only versions from a repository.
func majorOf(version string) string {
	if i := strings.IndexAny(version, ".+-"); i >= 0 {
		return version[:i]
	}
	return version
}

// newerInMajor lists the stable versions of key newer than current within
// current's major, newest first. Nil when current is not a published version
// (unknown ordering: never risk a downgrade).
func newerInMajor(key, current string, srcs []packages.PackageSource) ([]string, error) {
	groupID, artifactID, _ := strings.Cut(key, ":")
	published, err := listVersions(groupID, artifactID, srcs)
	if err != nil {
		return nil, err
	}
	order := packages.MatchingVersions(published, "") // newest (publish order) first
	currentIndex := slices.Index(order, current)
	if currentIndex < 0 {
		return nil, nil
	}
	var newer []string
	for _, v := range order[:currentIndex] {
		if isStableVersion(v) && majorOf(v) == majorOf(current) {
			newer = append(newer, v)
		}
	}
	return newer, nil
}

func clean(res packages.Resolution) bool {
	return len(res.Conflicts) == 0 && len(res.Missing) == 0
}

// PlanUpdates returns the newest STABLE version each declared dependency can
// move to WITHIN its current major (no major bumps) while keeping its
// configuration's transitive graph conflict-free and complete. Platforms move
// first (a newer BOM moves every managed version at once; the compile graph
// must stay clean under it), then the dependencies, checked against the moved
// platform set; a managed entry has no version of its own to bump. Port of
// planUpdates.
func PlanUpdates(cfg *config.Config, srcs []packages.PackageSource) ([]DependencyBump, error) {
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	platform := cfg.Dependencies // Platform/PlatformOrder are read, Platform is replaced per trial
	platform.Platform = maps.Clone(cfg.Dependencies.Platform)
	working := map[string]map[string]config.Dependency{}
	for _, name := range config.Configurations {
		working[name] = maps.Clone(cfg.Dependencies.Configuration(name))
	}
	// mainRoots is the one graph install resolves the main classpath as (see
	// sources.MainRoots), from the working maps.
	mainRoots := func() []packages.DependencyDeclaration {
		var roots []packages.DependencyDeclaration
		for _, name := range []string{"api", "implementation", "compileOnly", "runtimeOnly"} {
			roots = append(roots, sources.RootsOf(working[name])...)
		}
		return roots
	}
	// graphRoots is the graph a configuration's entries are checked in: the
	// main graph for the four classpath configurations, their own for
	// processors and tests.
	graphRoots := func(configuration string) []packages.DependencyDeclaration {
		switch configuration {
		case "annotationProcessor", "testImplementation":
			return sources.RootsOf(working[configuration])
		default:
			return mainRoots()
		}
	}
	// allClean reports whether every graph resolves clean and complete under
	// a managed set. A root the trial platform no longer manages rejects the
	// trial; any other resolve error propagates.
	allClean := func(managed map[packages.PackageKey]string) (bool, error) {
		graphs := [][]packages.DependencyDeclaration{mainRoots()}
		for _, name := range []string{"annotationProcessor", "testImplementation"} {
			if len(working[name]) > 0 {
				graphs = append(graphs, graphRoots(name))
			}
		}
		for _, roots := range graphs {
			if len(roots) == 0 {
				continue
			}
			res, err := packages.ResolveTransitive(roots, srcs, packages.ResolveOptions{Managed: managed})
			var unmanaged packages.UnmanagedRootError
			if errors.As(err, &unmanaged) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if !clean(res) {
				return false, nil
			}
		}
		return true, nil
	}

	var bumps []DependencyBump
	for _, key := range platform.PlatformKeys() {
		current := platform.Platform[key]
		newer, err := newerInMajor(key, current, srcs)
		if err != nil {
			return nil, err
		}
		for _, candidate := range newer[:min(updateAttempts, len(newer))] {
			trial := platform
			trial.Platform = maps.Clone(platform.Platform)
			trial.Platform[key] = candidate
			managed, err := sources.ManagedVersionsOf(trial, srcs)
			if err != nil {
				return nil, err
			}
			ok, err := allClean(managed)
			if err != nil {
				return nil, err
			}
			if ok {
				platform = trial
				bumps = append(bumps, DependencyBump{Configuration: config.PlatformConfiguration, Key: key, From: current, To: candidate})
				break
			}
		}
	}
	managed, err := sources.ManagedVersionsOf(platform, srcs)
	if err != nil {
		return nil, err
	}
	for _, configuration := range updateConfigs {
		for _, key := range sortedKeys(working[configuration]) {
			current := working[configuration][key]
			if current.Managed() {
				continue // the platform owns this version
			}
			newer, err := newerInMajor(key, current.Version, srcs)
			if err != nil {
				return nil, err
			}
			for _, candidate := range newer[:min(updateAttempts, len(newer))] {
				roots := withVersion(graphRoots(configuration), key, candidate)
				res, err := packages.ResolveTransitive(roots, srcs, packages.ResolveOptions{Managed: managed})
				if err != nil {
					return nil, err
				}
				if clean(res) {
					bumped := current
					bumped.Version = candidate
					working[configuration][key] = bumped
					bumps = append(bumps, DependencyBump{Configuration: configuration, Key: key, From: current.Version, To: candidate})
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

// PlanOutdated returns every declared dependency (platforms included) that has
// a newer published stable version, with the newest in-major version (Wanted)
// and newest overall (Latest). Managed entries have no version of their own and
// are skipped. Read-only. Port of planOutdated.
func PlanOutdated(cfg *config.Config, srcs []packages.PackageSource) ([]OutdatedDependency, error) {
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	type entry struct{ configuration, key, current string }
	var entries []entry
	for _, key := range cfg.Dependencies.PlatformKeys() {
		entries = append(entries, entry{config.PlatformConfiguration, key, cfg.Dependencies.Platform[key]})
	}
	for _, name := range config.Configurations {
		deps := cfg.Dependencies.Configuration(name)
		for _, key := range sortedKeys(deps) {
			if !deps[key].Managed() { // a managed entry has no version of its own
				entries = append(entries, entry{name, key, deps[key].Version})
			}
		}
	}
	var out []OutdatedDependency
	for _, e := range entries {
		groupID, artifactID, _ := strings.Cut(e.key, ":")
		published, err := listVersions(groupID, artifactID, srcs)
		if err != nil {
			return nil, err
		}
		order := packages.MatchingVersions(published, "") // newest first
		currentIndex := slices.Index(order, e.current)
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
		row := OutdatedDependency{Configuration: e.configuration, Key: e.key, Current: e.current}
		if latest := newer[0]; latest != e.current {
			row.Latest = latest
		}
		for _, v := range newer {
			if majorOf(v) == majorOf(e.current) {
				if v != e.current {
					row.Wanted = v
				}
				break
			}
		}
		out = append(out, row)
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
func withVersion(roots []packages.DependencyDeclaration, key, version string) []packages.DependencyDeclaration {
	out := make([]packages.DependencyDeclaration, len(roots))
	for i, c := range roots {
		if string(c.Coordinates.GAV().Key()) == key {
			c.Version = packages.Version(version)
		}
		out[i] = c
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
