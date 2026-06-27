package packages

// Transitive dependency resolution over an ordered list of package sources.
// Maven semantics where they matter: breadth-first "nearest wins" version
// selection (the version closest to a root is used; farther declarations of the
// same group:artifact are recorded as conflicts), and only non-optional
// compile/runtime dependencies propagate. Port of src/packages/resolver.ts.

import "strings"

// ResolvedPackage is one selected package and where it came from.
type ResolvedPackage struct {
	Coordinates Coordinates
	Metadata    PackageMetadata
	// Depth is 0 for the requested roots, 1 for their direct dependencies, ...
	Depth int
	// RequestedBy is which package declared it (zero value for a root).
	RequestedBy Coordinates
	// Source is the source that provided the metadata.
	Source SourceName
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
}

// ResolveTransitive resolves roots and their transitive dependencies against
// sources (consulted in order; the first source that knows a package provides
// it). onResolve, when non-nil, is called once per package as it is fetched.
func ResolveTransitive(roots []Coordinates, sources []PackageSource, onResolve func(Coordinates)) (Resolution, error) {
	res := Resolution{}
	// group:artifact -> selected version (nearest wins: BFS reaches near first)
	selected := map[PackageKey]string{}

	frontier := make([]frontierItem, 0, len(roots))
	for _, r := range roots {
		frontier = append(frontier, frontierItem{coordinates: r, isRoot: true})
	}

	for depth := 0; len(frontier) > 0; depth++ {
		var next []frontierItem
		for _, item := range frontier {
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
				continue // already resolved (or conflicting): never descend twice
			}
			if onResolve != nil {
				onResolve(item.coordinates)
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
			res.Packages = append(res.Packages, ResolvedPackage{
				Coordinates: item.coordinates,
				Metadata:    *meta,
				Depth:       depth,
				RequestedBy: item.requestedBy,
				Source:      source,
			})
			for _, dep := range meta.Dependencies {
				if !propagates(dep) {
					continue
				}
				next = append(next, frontierItem{
					coordinates: dep.Coordinates,
					requestedBy: item.coordinates,
				})
			}
		}
		frontier = next
	}
	return res, nil
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
	for _, p := range s.byKey[c.Key()] {
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
