package packages

// Transitive dependency resolution over an ordered list of package sources.
// Maven semantics where they matter: breadth-first "nearest wins" version
// selection (the version closest to a root is used; farther declarations of the
// same group:artifact are recorded as conflicts), and only non-optional
// compile/runtime dependencies propagate. Port of src/packages/resolver.ts.

import (
	"strings"
	"sync"
)

// ResolvedPackage is one selected package and where it came from.
type ResolvedPackage struct {
	Coordinates Coordinates
	Metadata    PackageMetadata
	// Depth is 0 for the requested roots, 1 for their direct dependencies, ...
	Depth int
	// RequestedBy is which package declared it (zero value for a root).
	RequestedBy Coordinates
	// AlsoRequestedBy are the other packages whose dependency edge resolved
	// to this package later in the walk: a request for the same version, or
	// for another one that lost to it (nearest wins makes that edge point
	// here, as in Maven's graph). Together with RequestedBy these are every
	// edge of the resolved graph.
	AlsoRequestedBy []Coordinates
	// Source is the source that provided the metadata.
	Source SourceName
	// Managed is true when a platform decided the version: a root declared
	// without one, or a transitive whose declared version it overrode.
	Managed bool
}

// UnmanagedRootError is returned by ResolveTransitive for a root declared
// without a version that no platform manages.
type UnmanagedRootError struct{ Key PackageKey }

func (e UnmanagedRootError) Error() string {
	return string(e.Key) + ": no version given and no platform manages it"
}

// ResolveOptions tunes ResolveTransitive.
type ResolveOptions struct {
	// Managed are the platform-managed versions (keyed like Coordinates.Key).
	// They fill a root declared without a version and override the version a
	// transitive POM declares - Maven's dependencyManagement semantics. A root
	// with an explicit version keeps it.
	Managed map[PackageKey]string
	// OnResolve, when non-nil, is called once per package as it is fetched.
	OnResolve func(Coordinates)
}

// VersionConflict records a same-package version clash; the nearer version won.
type VersionConflict struct {
	Key        PackageKey
	Selected   string
	Rejected   string
	RejectedBy Coordinates
}

// MissingPackage is a coordinate no source could provide.
type MissingPackage struct {
	Coordinates Coordinates
	RequestedBy Coordinates
}

// Resolution is the outcome of a transitive resolve.
type Resolution struct {
	// Packages are every selected package, in breadth-first discovery order.
	Packages []ResolvedPackage
	// Conflicts are same-package version clashes; the nearer version won.
	Conflicts []VersionConflict
	// Missing are coordinates no source could provide.
	Missing []MissingPackage
}

// propagates reports whether a declared dependency propagates to its consumer.
func propagates(d DependencyDeclaration) bool {
	return !d.Optional && (d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime")
}

func metadataFrom(sources []PackageSource, c Coordinates) (*PackageMetadata, SourceName, error) {
	for _, source := range sources {
		meta, err := source.GetMetadata(c)
		if err != nil {
			return nil, "", err
		}
		if meta != nil {
			return meta, source.Name(), nil
		}
	}
	return nil, "", nil
}

type frontierItem struct {
	coordinates Coordinates
	requestedBy Coordinates
	isRoot      bool
	managed     bool
	// exclusions are the patterns every ancestor edge excluded, so an
	// exclusion prunes the whole subtree below the edge that declared it (Maven
	// semantics: path-scoped, a package excluded here may still arrive via
	// another path).
	exclusions []string
}

// excluded reports whether c matches any "group:artifact" pattern; either side
// of a pattern may be "*".
func excluded(c Coordinates, patterns []string) bool {
	for _, pattern := range patterns {
		group, artifact, _ := strings.Cut(pattern, ":")
		if (group == "*" || group == string(c.GroupID)) && (artifact == "*" || artifact == string(c.ArtifactID)) {
			return true
		}
	}
	return false
}

// inheritExclusions is the parent's exclusions plus the edge's own.
func inheritExclusions(parent, own []string) []string {
	if len(own) == 0 {
		return parent
	}
	return append(append(make([]string, 0, len(parent)+len(own)), parent...), own...)
}

// resolveRange resolves a Maven version range (`[1.0,2.0)`, `RELEASE`, ...)
// declared in cappu.json or a transitive POM to a concrete published version:
// the highest published version satisfying the range (the first source that
// lists any version wins, like LatestVersion). A bare exact version is returned
// unchanged; ok is false for an unsatisfiable range so the caller records it as
// missing.
// ponytail: each range resolves independently to its highest match; Maven's
// soft/hard-requirement reconciliation across declarations is not implemented.
func resolveRange(c Coordinates, sources []PackageSource) (Coordinates, bool, error) {
	spec, isRange := ParseVersionSpec(string(c.Version))
	if !isRange {
		return c, true, nil // exact version: unchanged
	}
	for _, source := range sources {
		published, err := source.ListVersions(string(c.GroupID), string(c.ArtifactID))
		if err != nil {
			return Coordinates{}, false, err
		}
		if len(published) == 0 {
			continue
		}
		version := SelectVersion(spec, published)
		if version == "" {
			return Coordinates{}, false, nil
		}
		c.Version = Version(version) // classifier and extension stay
		return c, true, nil
	}
	return Coordinates{}, false, nil
}

// ResolveTransitive resolves roots (declared dependencies: coordinates plus
// their own exclusions) and their transitive dependencies against sources
// (consulted in order; the first source that knows a package provides it). A
// root without a version takes it from opts.Managed; one no platform manages
// is an error.
func ResolveTransitive(roots []DependencyDeclaration, sources []PackageSource, opts ResolveOptions) (Resolution, error) {
	res := Resolution{}
	// group:artifact -> selected version (nearest wins: BFS reaches near first)
	selected := map[PackageKey]string{}
	// key -> index into res.Packages, to record the later parents of a package
	index := map[PackageKey]int{}

	frontier := make([]frontierItem, 0, len(roots))
	// pinned are the roots with a version written out: cappu.json always wins
	// over the platform for them, so transitive requests of the same artifact
	// are left as declared (and mediate against the root as usual).
	pinned := map[PackageKey]bool{}
	for _, r := range roots {
		item := frontierItem{coordinates: r.Coordinates, isRoot: true, exclusions: r.Exclusions}
		if item.coordinates.Version == "" {
			version, ok := opts.Managed[item.coordinates.Key()]
			if !ok {
				return res, UnmanagedRootError{Key: item.coordinates.Key()}
			}
			item.coordinates.Version = Version(version)
			item.managed = true
		} else {
			pinned[item.coordinates.Key()] = true
		}
		frontier = append(frontier, item)
	}

	for depth := 0; len(frontier) > 0; depth++ {
		var next []frontierItem
		prefetchLevel(frontier, sources, selected)
		for _, item := range frontier {
			// Resolve a version range to a concrete published version first, so
			// the conflict/nearest-wins logic and the metadata fetch all see a
			// real version (two ranges resolving to the same version do not
			// conflict).
			concrete, ok, err := resolveRange(item.coordinates, sources)
			if err != nil {
				return res, err
			}
			if !ok {
				key := item.coordinates.Key()
				if _, seen := selected[key]; !seen {
					selected[key] = string(item.coordinates.Version) // do not retry / re-report
					res.Missing = append(res.Missing, MissingPackage{
						Coordinates: item.coordinates,
						RequestedBy: item.requestedBy,
					})
				}
				continue
			}
			item.coordinates = concrete
			key := item.coordinates.Key()
			if winner, ok := selected[key]; ok {
				if winner != string(item.coordinates.Version) {
					rejectedBy := item.requestedBy
					if item.isRoot {
						rejectedBy = item.coordinates
					}
					res.Conflicts = append(res.Conflicts, VersionConflict{
						Key:        key,
						Selected:   winner,
						Rejected:   string(item.coordinates.Version),
						RejectedBy: rejectedBy,
					})
				}
				if i, resolved := index[key]; resolved && !item.isRoot {
					res.Packages[i].AlsoRequestedBy = append(res.Packages[i].AlsoRequestedBy, item.requestedBy)
				}
				continue // already resolved (or conflicting): never descend twice
			}
			if opts.OnResolve != nil {
				opts.OnResolve(item.coordinates)
			}
			meta, source, err := metadataFrom(sources, item.coordinates)
			if err != nil {
				return res, err
			}
			if meta == nil {
				selected[key] = string(item.coordinates.Version) // do not retry / re-report
				res.Missing = append(res.Missing, MissingPackage{
					Coordinates: item.coordinates,
					RequestedBy: item.requestedBy,
				})
				continue
			}
			selected[key] = string(item.coordinates.Version)
			index[key] = len(res.Packages)
			res.Packages = append(res.Packages, ResolvedPackage{
				Coordinates: item.coordinates,
				Metadata:    *meta,
				Depth:       depth,
				RequestedBy: item.requestedBy,
				Source:      source,
				Managed:     item.managed,
			})
			for _, dep := range meta.Dependencies {
				if !propagates(dep) || excluded(dep.Coordinates, item.exclusions) {
					continue
				}
				child := frontierItem{
					coordinates: dep.Coordinates,
					requestedBy: item.coordinates,
					exclusions:  inheritExclusions(item.exclusions, dep.Exclusions),
				}
				// A platform-managed version replaces whatever the POM declared.
				if version, ok := opts.Managed[dep.Key()]; ok && !pinned[dep.Key()] && version != string(dep.Version) {
					child.coordinates.Version = Version(version)
					child.managed = true
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return res, nil
}

// prefetchLevel warms the source caches for a whole BFS level concurrently
// (bounded), so the ordered nearest-wins loop in ResolveTransitive keeps its
// exact sequential semantics while paying one round of network latency per
// level instead of one per package. Errors are ignored here; the sequential
// pass re-runs the (now cached) call and reports them in declaration order.
// The goroutines only read `selected`; the bookkeeping loop writes it after
// Wait returns.
func prefetchLevel(frontier []frontierItem, sources []PackageSource, selected map[PackageKey]string) {
	if len(frontier) < 2 {
		return
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, item := range frontier {
		wg.Add(1)
		go func(declared Coordinates) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			concrete, ok, err := resolveRange(declared, sources)
			if err != nil || !ok {
				return
			}
			if _, done := selected[concrete.Key()]; done {
				return
			}
			_, _, _ = metadataFrom(sources, concrete)
		}(item.coordinates)
	}
	wg.Wait()
}

// LatestVersion returns the newest published version of group:artifact across
// the sources.
func LatestVersion(groupID, artifactID string, sources []PackageSource) (string, error) {
	for _, source := range sources {
		versions, err := source.ListVersions(groupID, artifactID)
		if err != nil {
			return "", err
		}
		if len(versions) > 0 {
			return versions[len(versions)-1], nil
		}
	}
	return "", nil
}

// InMemoryPackageSource is an in-memory source: fixtures in tests, local
// overrides later.
type InMemoryPackageSource struct {
	name  string
	byKey map[PackageKey][]PackageMetadata
	keys  []PackageKey // insertion order, so Search is deterministic
	// managed holds what a BOM manages, for platform tests.
	managed map[CoordinateString]map[PackageKey]string
}

// SetManaged records the versions the BOM at c manages.
func (s *InMemoryPackageSource) SetManaged(c Coordinates, managed map[PackageKey]string) {
	if s.managed == nil {
		s.managed = map[CoordinateString]map[PackageKey]string{}
	}
	s.managed[c.String()] = managed
}

func (s *InMemoryPackageSource) GetManaged(c Coordinates) (map[PackageKey]string, error) {
	return s.managed[c.String()], nil
}

// NewInMemoryPackageSource builds an in-memory source from a list of metadata.
func NewInMemoryPackageSource(name string, pkgs []PackageMetadata) *InMemoryPackageSource {
	s := &InMemoryPackageSource{name: name, byKey: map[PackageKey][]PackageMetadata{}}
	for _, pkg := range pkgs {
		key := pkg.Coordinates.Key()
		if _, ok := s.byKey[key]; !ok {
			s.keys = append(s.keys, key)
		}
		s.byKey[key] = append(s.byKey[key], pkg)
	}
	return s
}

func (s *InMemoryPackageSource) Name() SourceName { return SourceName(s.name) }

func (s *InMemoryPackageSource) Search(query string) ([]SearchHit, error) {
	q := strings.ToLower(query)
	var hits []SearchHit
	for _, key := range s.keys {
		if strings.Contains(strings.ToLower(string(key)), q) {
			list := s.byKey[key]
			// the version count is the one extra fact an in-memory source knows
			count := len(list)
			hits = append(hits, SearchHit{Coordinates: list[len(list)-1].Coordinates, VersionCount: &count})
		}
	}
	return hits, nil
}

func (s *InMemoryPackageSource) ListVersions(groupID, artifactID string) ([]string, error) {
	list := s.byKey[Coordinates{GroupID: GroupID(groupID), ArtifactID: ArtifactID(artifactID)}.Key()]
	versions := make([]string, 0, len(list))
	for _, p := range list {
		versions = append(versions, string(p.Coordinates.Version))
	}
	return versions, nil
}

func (s *InMemoryPackageSource) GetMetadata(c Coordinates) (*PackageMetadata, error) {
	// A classified or pom-typed artifact shares the POM (and so the metadata) of its GAV.
	for _, p := range s.byKey[c.GAV().Key()] {
		if p.Coordinates.String() == c.String() {
			pkg := p
			return &pkg, nil
		}
	}
	return nil, nil
}

func (s *InMemoryPackageSource) GetArtifact(Coordinates) ([]byte, error) {
	return nil, nil
}

func (s *InMemoryPackageSource) GetPom(Coordinates) ([]byte, error) {
	return nil, nil
}
