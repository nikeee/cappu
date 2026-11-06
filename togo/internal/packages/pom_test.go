package packages

import (
	"reflect"
	"strings"
	"testing"
)

const testBase = "https://repo.example/maven2"

// mapFetcher serves POMs from a map keyed by the path under the repository root.
func mapFetcher(poms map[string]string) FetchText {
	return func(u string) (string, bool, error) {
		path := strings.TrimPrefix(u, testBase)
		if v, ok := poms[path]; ok {
			return v, true, nil
		}
		return "", false, nil
	}
}

func noBytes(string) ([]byte, bool, error) { return nil, false, nil }

func sourceWith(poms map[string]string) *MavenRepositorySource {
	return NewMavenRepositorySourceWithFetchers(testBase, "", mapFetcher(poms), noBytes)
}

func depDecl(spec, scope string, optional bool) DependencyDeclaration {
	return DependencyDeclaration{Coordinates: coord(spec), Scope: MavenScope(scope), Optional: optional}
}

const metadataXML = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <versioning>
    <versions>
      <version>3.12.0</version>
      <version>3.13.0</version>
      <version>3.14.0</version>
    </versions>
  </versioning>
</metadata>`

const pomXMLText = `<?xml version="1.0"?>
<project>
  <groupId>org.example</groupId>
  <artifactId>app</artifactId>
  <version>1.0</version>
  <description>Example app</description>
  <dependencyManagement>
    <dependencies>
      <dependency><groupId>org.managed</groupId><artifactId>managed</artifactId><version>9</version></dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency><groupId>org.apache.commons</groupId><artifactId>commons-lang3</artifactId><version>3.14.0</version></dependency>
    <dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId><version>5.10.0</version><scope>test</scope></dependency>
    <dependency><groupId>org.opt</groupId><artifactId>opt</artifactId><version>1</version><optional>true</optional></dependency>
    <dependency><groupId>org.prop</groupId><artifactId>prop</artifactId><version>${prop.version}</version></dependency>
  </dependencies>
</project>`

var coordsAppV1 = NewCoordinates("org.example", "app", "1.0")

func TestParseMetadataVersions(t *testing.T) {
	if got := parseMetadataVersions(metadataXML); !reflect.DeepEqual(got, []string{"3.12.0", "3.13.0", "3.14.0"}) {
		t.Errorf("versions = %v", got)
	}
	// Malformed XML yields no versions rather than an error.
	if got := parseMetadataVersions("not xml at all"); len(got) != 0 {
		t.Errorf("malformed metadata = %v, want empty", got)
	}
}

func TestParsePomDependencies(t *testing.T) {
	meta, incomplete := ParsePom(pomXMLText, coordsAppV1)
	if meta.Description != "Example app" {
		t.Errorf("description = %q", meta.Description)
	}
	want := []DependencyDeclaration{
		depDecl("org.apache.commons:commons-lang3:3.14.0", "", false),
		depDecl("org.junit.jupiter:junit-jupiter:5.10.0", "test", false),
		depDecl("org.opt:opt:1", "", true),
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v", meta.Dependencies)
	}
	if !incomplete { // the ${prop.version} dependency was dropped
		t.Error("expected incomplete = true")
	}
}

func TestParsePomHomepageAndScm(t *testing.T) {
	withURL := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <url>https://example.org/app</url>
    <scm><url>https://github.com/example/app</url></scm>
  </project>`
	a, _ := ParsePom(withURL, coordsAppV1)
	if a.Homepage != "https://example.org/app" {
		t.Errorf("homepage = %q", a.Homepage)
	}
	if a.ScmURL != "https://github.com/example/app" {
		t.Errorf("scmUrl = %q", a.ScmURL)
	}

	// No <scm><url>: fall back to <connection>, dropping the scm:<provider>: prefix.
	withConnection := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <scm><connection>scm:git:https://github.com/example/app.git</connection></scm>
  </project>`
	b, _ := ParsePom(withConnection, coordsAppV1)
	if b.Homepage != "" {
		t.Errorf("homepage = %q, want empty", b.Homepage)
	}
	if b.ScmURL != "https://github.com/example/app.git" {
		t.Errorf("scmUrl = %q", b.ScmURL)
	}
}

func TestParsePomLicenses(t *testing.T) {
	pom := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <licenses>
      <license><name>The Apache Software License, Version 2.0</name><url>https://www.apache.org/licenses/LICENSE-2.0.txt</url></license>
      <license><name>Public Domain</name></license>
    </licenses>
  </project>`
	meta, _ := ParsePom(pom, coordsAppV1)
	wantLic := []License{
		{Name: "The Apache Software License, Version 2.0", URL: "https://www.apache.org/licenses/LICENSE-2.0.txt"},
		{Name: "Public Domain"},
	}
	if !reflect.DeepEqual(meta.Licenses, wantLic) {
		t.Errorf("licenses = %+v", meta.Licenses)
	}
	if !reflect.DeepEqual(meta.LicenseNormalized, []SpdxID{"Apache-2.0"}) {
		t.Errorf("licenseNormalized = %v", meta.LicenseNormalized)
	}
}

func TestLicensesInheritedFromParent(t *testing.T) {
	source := sourceWith(map[string]string{
		"/org/example/app/1.0/app-1.0.pom":   `<project><parent><groupId>org.example</groupId><artifactId>parent</artifactId><version>7</version></parent></project>`,
		"/org/example/parent/7/parent-7.pom": `<project><licenses><license><name>MIT License</name></license></licenses></project>`,
	})
	meta, err := source.GetMetadata(coordsAppV1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(meta.Licenses, []License{{Name: "MIT License"}}) {
		t.Errorf("licenses = %+v", meta.Licenses)
	}
	if !reflect.DeepEqual(meta.LicenseNormalized, []SpdxID{"MIT"}) {
		t.Errorf("licenseNormalized = %v", meta.LicenseNormalized)
	}
}

func TestRepositoryLayoutURLs(t *testing.T) {
	var fetched []string
	fetch := func(u string) (string, bool, error) {
		fetched = append(fetched, u)
		switch {
		case strings.HasSuffix(u, "/maven-metadata.xml"):
			return metadataXML, true, nil
		case strings.HasSuffix(u, "/app-1.0.pom"):
			return pomXMLText, true, nil
		}
		return "", false, nil
	}
	source := NewMavenRepositorySourceWithFetchers("https://repo.example/maven2/", "", fetch, noBytes)

	versions, _ := source.ListVersions("org.apache.commons", "commons-lang3")
	if !reflect.DeepEqual(versions, []string{"3.12.0", "3.13.0", "3.14.0"}) {
		t.Errorf("versions = %v", versions)
	}
	if fetched[0] != "https://repo.example/maven2/org/apache/commons/commons-lang3/maven-metadata.xml" {
		t.Errorf("metadata url = %q", fetched[0])
	}
	meta, _ := source.GetMetadata(coordsAppV1)
	if fetched[1] != "https://repo.example/maven2/org/example/app/1.0/app-1.0.pom" {
		t.Errorf("pom url = %q", fetched[1])
	}
	if len(meta.Dependencies) != 3 {
		t.Errorf("deps = %d, want 3", len(meta.Dependencies))
	}
	if missing, _ := source.GetMetadata(NewCoordinates("org.example", "app", "0.404")); missing != nil {
		t.Errorf("missing version should be nil, got %+v", missing)
	}
	if hits, _ := source.Search("gson"); hits != nil { // no searchUrl
		t.Errorf("unsearchable source returned %v", hits)
	}
}

func TestSearchEncodesQueryAndToleratesBrokenAnswer(t *testing.T) {
	var fetched []string
	answer := `{"response":{"docs":[{"g":"com.google.code.gson","a":"gson","latestVersion":"2.13.1"},{"g":"org.partial","a":"no-version"}]}}`
	fetch := func(u string) (string, bool, error) {
		fetched = append(fetched, u)
		return answer, true, nil
	}
	source := NewMavenRepositorySourceWithFetchers("https://repo.example/maven2", "https://search.example/solrsearch/select", fetch, noBytes)

	hits, _ := source.Search("gso n")
	if !reflect.DeepEqual(hits, []SearchHit{{Coordinates: NewCoordinates("com.google.code.gson", "gson", "2.13.1")}}) {
		t.Errorf("hits = %+v", hits)
	}
	if fetched[0] != "https://search.example/solrsearch/select?q=gso+n&rows=20&wt=json" {
		t.Errorf("search url = %q", fetched[0])
	}
	answer = "not json at all"
	if hits, _ := source.Search("gson"); hits != nil {
		t.Errorf("broken answer should yield nil, got %v", hits)
	}
}

func TestPropertiesInterpolate(t *testing.T) {
	pom := `<project>
    <properties><lib.version>2.5</lib.version></properties>
    <dependencies>
      <dependency><groupId>g</groupId><artifactId>lib</artifactId><version>${lib.version}</version></dependency>
      <dependency><groupId>${project.groupId}</groupId><artifactId>sibling</artifactId><version>${project.version}</version></dependency>
    </dependencies>
  </project>`
	meta, incomplete := ParsePom(pom, coordsAppV1)
	if incomplete {
		t.Error("expected incomplete = false")
	}
	want := []DependencyDeclaration{
		depDecl("g:lib:2.5", "", false),
		depDecl("org.example:sibling:1.0", "", false),
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v", meta.Dependencies)
	}
}

func TestGetMetadataParentChain(t *testing.T) {
	poms := map[string]string{
		"/org/example/app/1.0/app-1.0.pom": `<project>
        <parent><groupId>org.example</groupId><artifactId>parent</artifactId><version>7</version></parent>
        <properties><lib.version>3.1</lib.version></properties>
        <dependencies>
          <dependency><groupId>g</groupId><artifactId>from-prop</artifactId><version>${lib.version}</version></dependency>
          <dependency><groupId>g</groupId><artifactId>from-mgmt</artifactId></dependency>
          <dependency><groupId>g</groupId><artifactId>unmanaged</artifactId></dependency>
        </dependencies>
      </project>`,
		"/org/example/parent/7/parent-7.pom": `<project>
        <parent><groupId>org.example</groupId><artifactId>grandparent</artifactId><version>1</version></parent>
        <properties><lib.version>9.9</lib.version><mgmt.version>4.2</mgmt.version></properties>
      </project>`,
		"/org/example/grandparent/1/grandparent-1.pom": `<project>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>from-mgmt</artifactId><version>${mgmt.version}</version></dependency>
          <dependency><groupId>bom</groupId><artifactId>imported</artifactId><version>1</version><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
      </project>`,
	}
	var fetched []string
	source := NewMavenRepositorySourceWithFetchers(testBase, "", func(u string) (string, bool, error) {
		fetched = append(fetched, u)
		path := strings.TrimPrefix(u, testBase)
		if v, ok := poms[path]; ok {
			return v, true, nil
		}
		return "", false, nil
	}, noBytes)

	meta, incomplete, err := source.getMetadata(coordsAppV1)
	if err != nil {
		t.Fatal(err)
	}
	want := []DependencyDeclaration{
		depDecl("g:from-prop:3.1", "", false), // child's own property wins
		depDecl("g:from-mgmt:4.2", "", false), // managed in grandparent, parent's property
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v", meta.Dependencies)
	}
	if !incomplete { // `unmanaged` has no version; import entries not followed for deps
		t.Error("expected incomplete = true")
	}
	before := len(fetched)
	_, _, _ = source.getMetadata(coordsAppV1) // cached: nothing new fetched
	if len(fetched) != before {
		t.Errorf("second resolve fetched %d new urls", len(fetched)-before)
	}
}

func TestGetMetadataBOMImports(t *testing.T) {
	poms := map[string]string{
		"/org/example/app/1.0/app-1.0.pom": `<project>
        <properties><bom.version>3</bom.version></properties>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>both</artifactId><version>0.1</version></dependency>
          <dependency><groupId>org.example</groupId><artifactId>bom</artifactId><version>${bom.version}</version><type>pom</type><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
        <dependencies>
          <dependency><groupId>g</groupId><artifactId>from-bom</artifactId></dependency>
          <dependency><groupId>g</groupId><artifactId>both</artifactId></dependency>
        </dependencies>
      </project>`,
		"/org/example/bom/3/bom-3.pom": `<project>
        <properties><lib.version>7.5</lib.version></properties>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>from-bom</artifactId><version>${lib.version}</version></dependency>
          <dependency><groupId>g</groupId><artifactId>both</artifactId><version>9.9</version></dependency>
          <dependency><groupId>org.example</groupId><artifactId>bom</artifactId><version>3</version><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
      </project>`,
	}
	source := sourceWith(poms)
	meta, incomplete, err := source.getMetadata(coordsAppV1)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete {
		t.Error("expected incomplete = false")
	}
	want := []DependencyDeclaration{
		depDecl("g:from-bom:7.5", "", false), // managed in the imported BOM
		depDecl("g:both:0.1", "", false),     // importing chain's own mgmt wins over the BOM
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v", meta.Dependencies)
	}
}

func TestCyclicOrMissingParentChain(t *testing.T) {
	poms := map[string]string{
		"/g/a/1/a-1.pom": `<project>
        <parent><groupId>g</groupId><artifactId>b</artifactId><version>1</version></parent>
        <dependencies><dependency><groupId>g</groupId><artifactId>dep</artifactId></dependency></dependencies>
      </project>`,
		"/g/b/1/b-1.pom": `<project>
        <parent><groupId>g</groupId><artifactId>a</artifactId><version>1</version></parent>
      </project>`,
		"/g/orphan/1/orphan-1.pom": `<project>
        <parent><groupId>g</groupId><artifactId>gone</artifactId><version>1</version></parent>
        <dependencies><dependency><groupId>g</groupId><artifactId>dep</artifactId></dependency></dependencies>
      </project>`,
	}
	source := sourceWith(poms)

	cyclic, incomplete, _ := source.getMetadata(NewCoordinates("g", "a", "1"))
	if len(cyclic.Dependencies) != 0 || !incomplete {
		t.Errorf("cyclic: deps=%v incomplete=%v", cyclic.Dependencies, incomplete)
	}
	orphan, incomplete2, _ := source.getMetadata(NewCoordinates("g", "orphan", "1"))
	if len(orphan.Dependencies) != 0 || !incomplete2 {
		t.Errorf("orphan: deps=%v incomplete=%v", orphan.Dependencies, incomplete2)
	}
}

func TestParsePomTypeClassifierExclusions(t *testing.T) {
	pom := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <properties><native.classifier>linux-x86_64</native.classifier></properties>
    <dependencies>
      <dependency><groupId>org.bom</groupId><artifactId>deps</artifactId><version>1</version><type>pom</type></dependency>
      <dependency><groupId>org.t</groupId><artifactId>t</artifactId><version>1</version><type>test-jar</type></dependency>
      <dependency><groupId>io.netty</groupId><artifactId>epoll</artifactId><version>4</version><classifier>${native.classifier}</classifier></dependency>
      <dependency><groupId>io.netty</groupId><artifactId>tcnative</artifactId><version>2</version><classifier>${os.detected.classifier}</classifier></dependency>
      <dependency><groupId>org.x</groupId><artifactId>x</artifactId><version>1</version>
        <exclusions>
          <exclusion><groupId>commons-logging</groupId><artifactId>commons-logging</artifactId></exclusion>
          <exclusion><groupId>org.slf4j</groupId></exclusion><!-- a missing side matches nothing: dropped -->
        </exclusions>
      </dependency>
    </dependencies>
  </project>`
	meta, incomplete := ParsePom(pom, coordsAppV1)
	if incomplete {
		t.Error("nothing should have been dropped")
	}
	want := []DependencyDeclaration{
		{Coordinates: pomOnly("org.bom:deps:1")},
		{Coordinates: coord("org.t:t:1").WithClassifier("tests")},
		{Coordinates: coord("io.netty:epoll:4").WithClassifier("linux-x86_64")},
		// an unresolvable classifier property falls back to the plain jar
		{Coordinates: coord("io.netty:tcnative:2")},
		{Coordinates: coord("org.x:x:1"), Exclusions: []string{"commons-logging:commons-logging"}},
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v\nwant %+v", meta.Dependencies, want)
	}
}

func pomOnly(spec string) Coordinates {
	c := coord(spec)
	c.Extension = "pom"
	return c
}

func TestParsePomManagedScopeAndExclusions(t *testing.T) {
	// <dependencyManagement> supplies what a dependency leaves unset (version,
	// scope, exclusions), keyed by g:a:classifier so a test-jar entry does not
	// clobber the plain one.
	pom := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <dependencyManagement><dependencies>
      <dependency><groupId>org.s</groupId><artifactId>core</artifactId><version>6</version>
        <exclusions><exclusion><groupId>commons-logging</groupId><artifactId>commons-logging</artifactId></exclusion></exclusions>
      </dependency>
      <dependency><groupId>org.s</groupId><artifactId>core</artifactId><version>5</version><type>test-jar</type><scope>test</scope></dependency>
      <dependency><groupId>org.o</groupId><artifactId>own</artifactId><version>1</version>
        <exclusions><exclusion><groupId>a</groupId><artifactId>a</artifactId></exclusion></exclusions>
      </dependency>
    </dependencies></dependencyManagement>
    <dependencies>
      <dependency><groupId>org.s</groupId><artifactId>core</artifactId></dependency>
      <dependency><groupId>org.s</groupId><artifactId>core</artifactId><type>test-jar</type></dependency>
      <dependency><groupId>org.o</groupId><artifactId>own</artifactId>
        <exclusions><exclusion><groupId>b</groupId><artifactId>b</artifactId></exclusion></exclusions>
      </dependency>
    </dependencies>
  </project>`
	meta, incomplete := ParsePom(pom, coordsAppV1)
	if incomplete {
		t.Error("every dependency is managed")
	}
	want := []DependencyDeclaration{
		{Coordinates: coord("org.s:core:6"), Exclusions: []string{"commons-logging:commons-logging"}},
		{Coordinates: coord("org.s:core:5").WithClassifier("tests"), Scope: "test"},
		// own exclusions win over the managed ones (Maven merges only when unset)
		{Coordinates: coord("org.o:own:1"), Exclusions: []string{"b:b"}},
	}
	if !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v\nwant %+v", meta.Dependencies, want)
	}
}

func TestGetManagedMergesChainAndImports(t *testing.T) {
	poms := map[string]string{
		"/org/x/parent/1/parent-1.pom": `<project>
      <groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version>
      <properties><lib.version>1.5</lib.version></properties>
      <dependencyManagement><dependencies>
        <dependency><groupId>org.l</groupId><artifactId>lib</artifactId><version>${lib.version}</version></dependency>
        <dependency><groupId>org.imp</groupId><artifactId>bom</artifactId><version>7</version><type>pom</type><scope>import</scope></dependency>
      </dependencies></dependencyManagement>
    </project>`,
		"/org/x/bom/2/bom-2.pom": `<project>
      <parent><groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version></parent>
      <artifactId>bom</artifactId>
      <dependencyManagement><dependencies>
        <dependency><groupId>org.l</groupId><artifactId>other</artifactId><version>${project.version}</version></dependency>
        <dependency><groupId>org.i</groupId><artifactId>x</artifactId><version>9</version></dependency>
        <dependency><groupId>io.n</groupId><artifactId>n</artifactId><version>4</version><classifier>linux-x86_64</classifier></dependency>
      </dependencies></dependencyManagement>
    </project>`,
		"/org/imp/bom/7/bom-7.pom": `<project>
      <groupId>org.imp</groupId><artifactId>bom</artifactId><version>7</version>
      <dependencyManagement><dependencies>
        <dependency><groupId>org.i</groupId><artifactId>x</artifactId><version>1</version></dependency>
        <dependency><groupId>org.i</groupId><artifactId>y</artifactId><version>2</version></dependency>
      </dependencies></dependencyManagement>
    </project>`,
	}
	src := sourceWith(poms)
	got, err := src.GetManaged(NewCoordinates("org.x", "bom", "2"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[PackageKey]string{
		"org.l:lib":           "1.5", // parent, property interpolated
		"org.l:other":         "2",   // ${project.version} of the BOM itself
		"org.i:x":             "9",   // the chain's own entry beats the import
		"org.i:y":             "2",   // from the imported BOM
		"io.n:n:linux-x86_64": "4",   // keyed like Coordinates.Key
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("managed = %v\nwant %v", got, want)
	}
	if unknown, err := src.GetManaged(NewCoordinates("org.x", "nope", "1")); err != nil || unknown != nil {
		t.Errorf("unknown BOM = %v, %v; want nil", unknown, err)
	}
}

func TestParsePomManagedLookupUsesInterpolatedClassifier(t *testing.T) {
	pom := `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <properties><native.classifier>linux-x86_64</native.classifier></properties>
    <dependencyManagement><dependencies>
      <dependency><groupId>io.netty</groupId><artifactId>epoll</artifactId><version>4</version><classifier>linux-x86_64</classifier></dependency>
    </dependencies></dependencyManagement>
    <dependencies>
      <dependency><groupId>io.netty</groupId><artifactId>epoll</artifactId><classifier>${native.classifier}</classifier></dependency>
    </dependencies>
  </project>`
	meta, incomplete := ParsePom(pom, coordsAppV1)
	want := []DependencyDeclaration{{Coordinates: coord("io.netty:epoll:4").WithClassifier("linux-x86_64")}}
	if incomplete || !reflect.DeepEqual(meta.Dependencies, want) {
		t.Errorf("dependencies = %+v (incomplete %v), want %+v", meta.Dependencies, incomplete, want)
	}
}
