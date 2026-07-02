package packages

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/nikeee/cappu/internal/httpx"
)

// A PackageSource over a maven2 repository layout (Maven Central, ...):
// maven-metadata.xml lists versions, the .pom carries the declared
// dependencies. POMs are resolved EFFECTIVELY: getMetadata walks the <parent>
// chain, merges properties child-over-parent, interpolates ${...}, and fills
// missing dependency versions from <dependencyManagement> (including scope=import
// BOMs). Whatever still lacks a version is dropped and flagged via `incomplete`.
// The fetchers are injectable so everything is testable without a network. Port
// of src/packages/maven.ts.

// FetchText returns a url's body; found is false for a 404-ish miss.
type FetchText func(url string) (text string, found bool, err error)

// FetchBytes returns a url's bytes; found is false for a 404-ish miss.
type FetchBytes func(url string) (data []byte, found bool, err error)

// MavenRepositorySource implements PackageSource over a maven2 repository.
type MavenRepositorySource struct {
	baseURL    string
	searchURL  string // empty when the repository has no index service
	fetchText  FetchText
	fetchBytes FetchBytes
	// pomCache holds fetched+parsed POMs (nil pom = known miss), keyed by coords.
	pomCache map[CoordinateString]*RawPom
	seen     map[CoordinateString]bool
	// pomText holds the raw POM text as fetched (for GetPom), keyed by coords.
	pomText map[CoordinateString]string
	// mu guards the cache maps: ResolveTransitive prefetches a BFS level with
	// concurrent goroutines, all hitting the same source.
	mu sync.Mutex
	// versions holds published versions per "group:artifact" (maven-metadata.xml
	// is fetched once per coordinate).
	versions map[string][]string
}

// NewMavenRepositorySource builds a source for baseURL using HTTP fetchers.
// searchURL is the solr index endpoint, or "" for a repository without one.
func NewMavenRepositorySource(baseURL, searchURL string) *MavenRepositorySource {
	return NewMavenRepositorySourceWithFetchers(baseURL, searchURL, httpFetchText(httpx.Client), httpFetchBytes(httpx.Client))
}

// NewMavenRepositorySourceWithFetchers builds a source with injected fetchers
// (tests, local overrides).
func NewMavenRepositorySourceWithFetchers(baseURL, searchURL string, fetchText FetchText, fetchBytes FetchBytes) *MavenRepositorySource {
	return &MavenRepositorySource{
		baseURL:    baseURL,
		searchURL:  searchURL,
		fetchText:  fetchText,
		fetchBytes: fetchBytes,
		pomCache:   map[CoordinateString]*RawPom{},
		seen:       map[CoordinateString]bool{},
		pomText:    map[CoordinateString]string{},
		versions:   map[string][]string{},
	}
}

// Name is the repository url (a stable display name).
func (s *MavenRepositorySource) Name() SourceName { return SourceName(s.baseURL) }

func httpFetchText(client *http.Client) FetchText {
	return func(u string) (string, bool, error) {
		body, found, err := httpx.Get(client, u)
		return string(body), found, err
	}
}

func httpFetchBytes(client *http.Client) FetchBytes {
	return func(u string) ([]byte, bool, error) {
		return httpx.Get(client, u)
	}
}

// --- repository URLs ---------------------------------------------------------

func (s *MavenRepositorySource) repositoryURL(segments ...string) string {
	base := strings.TrimSuffix(s.baseURL, "/")
	return base + "/" + strings.Join(segments, "/")
}

func artifactPath(groupID, artifactID string) string {
	return strings.ReplaceAll(groupID, ".", "/") + "/" + artifactID
}

type searchDoc struct {
	Response struct {
		Docs []struct {
			G             string `json:"g"`
			A             string `json:"a"`
			LatestVersion string `json:"latestVersion"`
			// The index reports packaging as `p`, the total version count and a
			// last-published `timestamp` (epoch ms); all are best-effort extras.
			P            string `json:"p"`
			VersionCount *int   `json:"versionCount"`
			Timestamp    *int64 `json:"timestamp"`
		} `json:"docs"`
	} `json:"response"`
}

var coordinateQuery = regexp.MustCompile(`^([^\s:]+):([^\s:]+)$`)

// toSolrQuery translates a "group:artifact" coordinate into the structured Solr
// form, since `:` is Solr's field separator and would otherwise be misread.
// Anything else passes through as free text. Port of toSolrQuery in src/packages/maven.ts.
func toSolrQuery(query string) string {
	if m := coordinateQuery.FindStringSubmatch(query); m != nil {
		return `g:"` + m[1] + `" AND a:"` + m[2] + `"`
	}
	return query
}

// Search runs a free-text query via the index service; nil without one (or on a
// broken answer - a bad index reply must not fail the command).
func (s *MavenRepositorySource) Search(query string) ([]SearchHit, error) {
	if s.searchURL == "" {
		return nil, nil
	}
	u, err := url.Parse(s.searchURL)
	if err != nil {
		return nil, nil
	}
	u.RawQuery = url.Values{"q": {toSolrQuery(query)}, "rows": {"20"}, "wt": {"json"}}.Encode()
	text, found, err := s.fetchText(u.String())
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	var doc searchDoc
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil
	}
	var hits []SearchHit
	for _, d := range doc.Response.Docs {
		if d.G != "" && d.A != "" && d.LatestVersion != "" {
			hits = append(hits, SearchHit{
				Coordinates:  NewCoordinates(d.G, d.A, d.LatestVersion),
				Packaging:    d.P,
				VersionCount: d.VersionCount,
				LastUpdated:  d.Timestamp,
			})
		}
	}
	return hits, nil
}

// ListVersions returns all versions from maven-metadata.xml, oldest first.
func (s *MavenRepositorySource) ListVersions(groupID, artifactID string) ([]string, error) {
	key := groupID + ":" + artifactID
	s.mu.Lock()
	cached, ok := s.versions[key]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}
	text, found, err := s.fetchText(s.repositoryURL(artifactPath(groupID, artifactID), "maven-metadata.xml"))
	if err != nil {
		return nil, err // a transport error is not cached; a retry may succeed
	}
	var parsed []string
	if found {
		parsed = parseMetadataVersions(text)
	}
	s.mu.Lock()
	s.versions[key] = parsed
	s.mu.Unlock()
	return parsed, nil
}

// GetArtifact returns the package's jar bytes, or nil for a miss.
func (s *MavenRepositorySource) GetArtifact(c Coordinates) ([]byte, error) {
	data, found, err := s.fetchBytes(s.repositoryURL(
		artifactPath(string(c.GroupID), string(c.ArtifactID)),
		string(c.Version),
		c.ArtifactJarName(),
	))
	if err != nil || !found {
		return nil, err
	}
	return data, nil
}

// GetMetadata returns the effective metadata for coordinates, walking the
// parent chain and BOM imports. nil when the POM cannot be fetched.
func (s *MavenRepositorySource) GetMetadata(c Coordinates) (*PackageMetadata, error) {
	meta, _, err := s.getMetadata(c)
	return meta, err
}

// getMetadata also reports `incomplete` (a dependency dropped for lack of a
// resolvable version) - used by tests; the interface method drops it.
func (s *MavenRepositorySource) getMetadata(c Coordinates) (*PackageMetadata, bool, error) {
	chain, err := s.chainFor(c)
	if err != nil {
		return nil, false, err
	}
	if chain == nil {
		return nil, false, nil
	}
	imported, err := s.importedManaged(chain, c, map[CoordinateString]bool{})
	if err != nil {
		return nil, false, err
	}
	meta, incomplete := effectiveMetadata(chain, c, imported)
	return &meta, incomplete, nil
}

const parentChainLimit = 16 // generous; real chains are 2-4 deep

// cachedPom returns the cached parse for key, with ok=false on a cache miss.
func (s *MavenRepositorySource) cachedPom(key CoordinateString) (*RawPom, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[key] {
		return s.pomCache[key], true
	}
	return nil, false
}

// rawPom fetches and parses one POM (cached; nil = known miss).
func (s *MavenRepositorySource) rawPom(c Coordinates) (*RawPom, error) {
	key := c.String()
	if pom, ok := s.cachedPom(key); ok {
		return pom, nil
	}
	text, found, err := s.fetchText(s.repositoryURL(
		artifactPath(string(c.GroupID), string(c.ArtifactID)),
		string(c.Version),
		string(c.ArtifactID)+"-"+string(c.Version)+".pom",
	))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[key] = true
	if !found {
		s.pomCache[key] = nil
		return nil, nil
	}
	s.pomText[key] = text
	pom := parseRawPom(text)
	s.pomCache[key] = pom
	return pom, nil
}

// GetPom returns the package's own POM bytes. rawPom caches the text, so when
// GetMetadata has already run this is served from memory without a second fetch.
func (s *MavenRepositorySource) GetPom(c Coordinates) ([]byte, error) {
	key := c.String()
	if _, ok := s.cachedPom(key); !ok {
		if _, err := s.rawPom(c); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	text, ok := s.pomText[key]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return []byte(text), nil
}

// chainFor returns the parent chain of one POM, child first; nil when the child
// POM is missing.
func (s *MavenRepositorySource) chainFor(c Coordinates) ([]*RawPom, error) {
	child, err := s.rawPom(c)
	if err != nil || child == nil {
		return nil, err
	}
	chain := []*RawPom{child}
	seen := map[CoordinateString]bool{c.String(): true}
	parent := child.Parent
	for parent != nil && len(chain) < parentChainLimit {
		key := parent.String()
		if seen[key] {
			break // a cyclic chain must not loop
		}
		seen[key] = true
		pom, err := s.rawPom(*parent)
		if err != nil {
			return nil, err
		}
		if pom == nil {
			break
		}
		chain = append(chain, pom)
		parent = pom.Parent
	}
	return chain, nil
}

// importedManaged returns the managed versions a chain pulls in via scope=import
// BOMs, fully interpolated. Precedence is Maven's: a nearer import wins.
func (s *MavenRepositorySource) importedManaged(chain []*RawPom, c Coordinates, seen map[CoordinateString]bool) (map[string]string, error) {
	properties := mergedProperties(chain)
	result := map[string]string{}
	absorb := func(entries map[string]string) {
		for k, v := range entries {
			if _, ok := result[k]; !ok {
				result[k] = v
			}
		}
	}
	for _, pom := range chain {
		for _, imp := range pom.BomImports {
			if imp.GroupID == "" || imp.ArtifactID == "" || imp.Version == "" {
				continue
			}
			bom := NewCoordinates(
				interpolate(imp.GroupID, properties, c),
				interpolate(imp.ArtifactID, properties, c),
				interpolate(imp.Version, properties, c),
			)
			if strings.Contains(string(bom.GroupID)+string(bom.ArtifactID)+string(bom.Version), "${") {
				continue
			}
			key := bom.String()
			if seen[key] {
				continue // import cycles must not loop
			}
			seen[key] = true
			bomChain, err := s.chainFor(bom)
			if err != nil {
				return nil, err
			}
			if bomChain == nil {
				continue
			}
			// the BOM's managed entries, interpolated in the BOM's own context
			bomProperties := mergedProperties(bomChain)
			bomManaged := map[string]string{}
			for i := len(bomChain) - 1; i >= 0; i-- {
				for managedKey, raw := range bomChain[i].Managed {
					version := interpolate(raw, bomProperties, bom)
					if !strings.Contains(version, "${") {
						bomManaged[managedKey] = version
					}
				}
			}
			absorb(bomManaged)
			nested, err := s.importedManaged(bomChain, bom, seen)
			if err != nil {
				return nil, err
			}
			absorb(nested)
		}
	}
	return result, nil
}

// --- POM parsing -------------------------------------------------------------

// RawPom is one pom.xml as written: nothing inherited, nothing interpolated.
type RawPom struct {
	Parent       *Coordinates
	Properties   map[string]string
	Dependencies []rawDependency
	// Managed maps group:artifact -> raw version from <dependencyManagement>.
	Managed map[string]string
	// BomImports are the scope=import entries in <dependencyManagement>.
	BomImports  []rawDependency
	Description string
	// Homepage (<url>) and ScmURL (<scm>) as written.
	Homepage string
	ScmURL   string
	// Licenses as written; empty when the POM declares none (then inherited).
	Licenses []License
}

type rawDependency struct {
	GroupID    string
	ArtifactID string
	Version    string
	Scope      string
	Optional   string
}

// xmlProperties captures the arbitrary child elements of <properties>.
type xmlProperties map[string]string

func (p *xmlProperties) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	m := map[string]string{}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch el := tok.(type) {
		case xml.StartElement:
			var val string
			if err := d.DecodeElement(&val, &el); err != nil {
				return err
			}
			m[el.Name.Local] = val
		case xml.EndElement:
			if el.Name == start.Name {
				*p = m
				return nil
			}
		}
	}
}

type xmlDependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Optional   string `xml:"optional"`
}

type xmlProject struct {
	Parent *struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	} `xml:"parent"`
	Properties  xmlProperties `xml:"properties"`
	Description string        `xml:"description"`
	URL         string        `xml:"url"`
	Scm         struct {
		URL                 string `xml:"url"`
		Connection          string `xml:"connection"`
		DeveloperConnection string `xml:"developerConnection"`
	} `xml:"scm"`
	Dependencies struct {
		Dependency []xmlDependency `xml:"dependency"`
	} `xml:"dependencies"`
	DependencyManagement struct {
		Dependencies struct {
			Dependency []xmlDependency `xml:"dependency"`
		} `xml:"dependencies"`
	} `xml:"dependencyManagement"`
	Licenses struct {
		License []struct {
			Name string `xml:"name"`
			URL  string `xml:"url"`
		} `xml:"license"`
	} `xml:"licenses"`
}

func toRawDependency(d xmlDependency) rawDependency {
	return rawDependency(d)
}

// parseMetadataVersions returns all versions from a maven-metadata.xml, oldest
// first (document order).
func parseMetadataVersions(text string) []string {
	var doc struct {
		Versioning struct {
			Versions struct {
				Version []string `xml:"version"`
			} `xml:"versions"`
		} `xml:"versioning"`
	}
	if err := xml.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	return doc.Versioning.Versions.Version
}

// parseRawPom parses one pom.xml into its raw (uninterpolated) view.
func parseRawPom(text string) *RawPom {
	var project xmlProject
	if err := xml.Unmarshal([]byte(text), &project); err != nil {
		return &RawPom{Properties: map[string]string{}, Managed: map[string]string{}}
	}

	var parent *Coordinates
	if p := project.Parent; p != nil && p.GroupID != "" && p.ArtifactID != "" && p.Version != "" {
		c := NewCoordinates(p.GroupID, p.ArtifactID, p.Version)
		parent = &c
	}

	managed := map[string]string{}
	var bomImports []rawDependency
	for _, dep := range project.DependencyManagement.Dependencies.Dependency {
		if dep.GroupID == "" || dep.ArtifactID == "" || dep.Version == "" {
			continue
		}
		if dep.Scope == "import" {
			bomImports = append(bomImports, toRawDependency(dep))
		} else {
			managed[dep.GroupID+":"+dep.ArtifactID] = dep.Version
		}
	}

	var licenses []License
	for _, l := range project.Licenses.License {
		if l.Name != "" {
			licenses = append(licenses, License{Name: l.Name, URL: l.URL})
		}
	}

	deps := make([]rawDependency, 0, len(project.Dependencies.Dependency))
	for _, d := range project.Dependencies.Dependency {
		deps = append(deps, toRawDependency(d))
	}

	props := map[string]string(project.Properties)
	if props == nil {
		props = map[string]string{}
	}
	return &RawPom{
		Parent:       parent,
		Properties:   props,
		Dependencies: deps,
		Managed:      managed,
		BomImports:   bomImports,
		Description:  project.Description,
		Homepage:     project.URL,
		ScmURL:       scmURLOf(project.Scm.URL, project.Scm.Connection, project.Scm.DeveloperConnection),
		Licenses:     licenses,
	}
}

var scmPrefixRe = regexp.MustCompile(`^scm:[^:]*:`)

// scmURLOf resolves a POM <scm> url: prefer <url>, else <connection> with a
// leading `scm:<provider>:` stripped. Port of scmUrlOf in src/packages/maven.ts.
func scmURLOf(url, connection, developerConnection string) string {
	if url != "" {
		return url
	}
	conn := connection
	if conn == "" {
		conn = developerConnection
	}
	if conn == "" {
		return ""
	}
	return scmPrefixRe.ReplaceAllString(conn, "")
}

var interpolateRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// interpolate expands ${name} over the merged property map plus the project
// builtins; nested values resolve up to a small depth.
func interpolate(value string, properties map[string]string, project Coordinates) string {
	current := value
	for depth := 0; depth < 5 && strings.Contains(current, "${"); depth++ {
		current = interpolateRe.ReplaceAllStringFunc(current, func(whole string) string {
			name := strings.TrimSpace(interpolateRe.FindStringSubmatch(whole)[1])
			switch name {
			case "project.version", "version", "pom.version":
				return string(project.Version)
			case "project.groupId", "groupId", "pom.groupId":
				return string(project.GroupID)
			case "project.artifactId":
				return string(project.ArtifactID)
			}
			if v, ok := properties[name]; ok {
				return v
			}
			return whole
		})
	}
	return current
}

// mergedProperties merges a chain's properties child-over-parent (child wins).
func mergedProperties(chain []*RawPom) map[string]string {
	properties := map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Properties {
			properties[k] = v
		}
	}
	return properties
}

// effectiveMetadata is the effective dependency list of a POM chain (child
// first): properties merged child-over-parent, versions interpolated and filled
// from <dependencyManagement> (chain entries first, then imported BOMs). The
// bool reports whether any dependency was dropped for lack of a version.
func effectiveMetadata(chain []*RawPom, c Coordinates, imported map[string]string) (PackageMetadata, bool) {
	properties := mergedProperties(chain)

	managed := map[string]string{}
	for k, v := range imported {
		managed[k] = v
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Managed {
			managed[k] = v
		}
	}

	var child *RawPom
	if len(chain) > 0 {
		child = chain[0]
	}
	var dependencies []DependencyDeclaration
	incomplete := false
	if child != nil {
		for _, dep := range child.Dependencies {
			groupID := ""
			if dep.GroupID != "" {
				groupID = interpolate(dep.GroupID, properties, c)
			}
			artifactID := ""
			if dep.ArtifactID != "" {
				artifactID = interpolate(dep.ArtifactID, properties, c)
			}
			if groupID == "" || artifactID == "" {
				continue
			}
			raw := dep.Version
			if raw == "" {
				raw = managed[groupID+":"+artifactID]
			}
			if raw == "" {
				incomplete = true // unmanaged
				continue
			}
			version := interpolate(raw, properties, c)
			if strings.Contains(version, "${") {
				incomplete = true // beyond our property model
				continue
			}
			dependencies = append(dependencies, DependencyDeclaration{
				Coordinates: NewCoordinates(groupID, artifactID, version),
				Scope:       MavenScope(dep.Scope),
				Optional:    dep.Optional == "true",
			})
		}
	}

	// Maven does not merge <licenses>: the effective licenses are the nearest
	// chain entry (child first) that declares any.
	var licenses []License
	for _, pom := range chain {
		if len(pom.Licenses) > 0 {
			licenses = pom.Licenses
			break
		}
	}
	// Homepage/scm: the nearest chain entry (child first) that declares one,
	// interpolated like every other POM value.
	nearest := func(pick func(*RawPom) string) string {
		for _, pom := range chain {
			if v := pick(pom); v != "" {
				return interpolate(v, properties, c)
			}
		}
		return ""
	}

	meta := PackageMetadata{
		Coordinates:       c,
		Homepage:          nearest(func(p *RawPom) string { return p.Homepage }),
		ScmURL:            nearest(func(p *RawPom) string { return p.ScmURL }),
		Dependencies:      dependencies,
		Licenses:          licenses,
		LicenseNormalized: NormalizeLicenses(licenses),
	}
	if child != nil {
		meta.Description = child.Description
	}
	return meta, incomplete
}

// ParsePom is a single-pom convenience (tests/tooling): the effective view of
// one POM without its parents (parent-managed versions stay unresolved). The
// bool reports whether any dependency was dropped for lack of a version.
func ParsePom(text string, c Coordinates) (PackageMetadata, bool) {
	return effectiveMetadata([]*RawPom{parseRawPom(text)}, c, map[string]string{})
}
