package audit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

func coord(spec string) packages.Coordinates {
	p := strings.Split(spec, ":")
	return packages.NewCoordinates(p[0], p[1], p[2])
}

// vulns serves the canned vuln details, mirroring osv.test.ts VULNS.
var vulns = map[string]string{
	"GHSA-aaaa": `{"id":"GHSA-aaaa","summary":"RCE in foo","aliases":["CVE-2021-1","GHSA-aaaa"],"database_specific":{"severity":"CRITICAL"},"affected":[{"package":{"name":"org.foo:foo"},"ranges":[{"events":[{"introduced":"1.0"},{"fixed":"1.5"}]}]}]}`,
	"GHSA-bbbb": `{"id":"GHSA-bbbb","summary":"XXE in bar","aliases":["CVE-2022-2"],"database_specific":{"severity":"MODERATE"},"affected":[]}`,
}

// fakeOsv serves canned id lists + vuln details and records the batch queries.
func fakeOsv() (FetchJSON, *int) {
	queries := 0
	fetch := func(url string, body any) ([]byte, error) {
		if strings.HasSuffix(url, "/v1/querybatch") {
			queries++
			raw, _ := json.Marshal(body)
			var b struct {
				Queries []struct {
					Package struct{ Name string } `json:"package"`
				} `json:"queries"`
			}
			_ = json.Unmarshal(raw, &b)
			var results []string
			for _, q := range b.Queries {
				switch q.Package.Name {
				case "org.foo:foo":
					results = append(results, `{"vulns":[{"id":"GHSA-aaaa"}]}`)
				case "org.bar:bar":
					results = append(results, `{"vulns":[{"id":"GHSA-aaaa"},{"id":"GHSA-bbbb"}]}`) // shares GHSA-aaaa
				default:
					results = append(results, `{}`) // clean
				}
			}
			return []byte(`{"results":[` + strings.Join(results, ",") + `]}`), nil
		}
		_, id, _ := strings.Cut(url, "/v1/vulns/")
		return []byte(vulns[id]), nil
	}
	return fetch, &queries
}

func ids(advisories []Advisory) []string {
	out := make([]string, len(advisories))
	for i, a := range advisories {
		out[i] = string(a.ID)
	}
	return out
}

func TestOsvMapsAndHydratesOnce(t *testing.T) {
	fetch, queries := fakeOsv()
	hydrations := 0
	counting := func(url string, body any) ([]byte, error) {
		if strings.Contains(url, "/v1/vulns/") {
			hydrations++
		}
		return fetch(url, body)
	}
	source := NewOsvSource(counting)

	coords := []packages.Coordinates{coord("org.foo:foo:1.2"), coord("org.bar:bar:2.0"), coord("org.clean:clean:9.0")}
	result, err := source.FindVulnerabilities(coords)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(result["org.foo:foo:1.2"]); !reflect.DeepEqual(got, []string{"GHSA-aaaa"}) {
		t.Errorf("foo advisories = %v", got)
	}
	if got := ids(result["org.bar:bar:2.0"]); !reflect.DeepEqual(got, []string{"GHSA-aaaa", "GHSA-bbbb"}) {
		t.Errorf("bar advisories = %v", got)
	}
	if _, ok := result["org.clean:clean:9.0"]; ok {
		t.Error("clean package should have no entry")
	}
	// GHSA-aaaa is shared by two packages but fetched only once.
	if hydrations != 2 {
		t.Errorf("hydrations = %d, want 2", hydrations)
	}
	if *queries != 1 {
		t.Errorf("querybatch calls = %d, want 1", *queries)
	}
	foo := result["org.foo:foo:1.2"][0]
	if foo.Severity != SeverityCritical {
		t.Errorf("foo severity = %v", foo.Severity)
	}
	if !reflect.DeepEqual(foo.Aliases, []string{"CVE-2021-1"}) {
		t.Errorf("foo aliases = %v", foo.Aliases)
	}
	if !reflect.DeepEqual(foo.FixedVersions, []string{"1.5"}) {
		t.Errorf("foo fixed = %v", foo.FixedVersions)
	}
	if foo.URL != "https://osv.dev/vulnerability/GHSA-aaaa" {
		t.Errorf("foo url = %q", foo.URL)
	}
}

func TestCachedFetchJSON(t *testing.T) {
	t.Setenv("CAPPU_PACKAGE_STORE", t.TempDir())
	calls := 0
	inner := func(url string, body any) ([]byte, error) {
		calls++
		if body == nil {
			return []byte(`{"id":"GHSA-x","summary":"s"}`), nil
		}
		return []byte(`{"results":[]}`), nil
	}
	cached := CachedFetchJSON(inner)

	first, _ := cached("https://api.osv.dev/v1/vulns/GHSA-x", nil)
	second, _ := cached("https://api.osv.dev/v1/vulns/GHSA-x", nil)
	if !reflect.DeepEqual(first, second) || calls != 1 {
		t.Errorf("vuln detail not cached: calls=%d", calls)
	}
	// the querybatch lookup is never cached: fresh findings must surface
	_, _ = cached("https://api.osv.dev/v1/querybatch", map[string]any{"queries": []any{}})
	_, _ = cached("https://api.osv.dev/v1/querybatch", map[string]any{"queries": []any{}})
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (querybatch uncached)", calls)
	}
}

func TestSeverityAliasesFixedVersions(t *testing.T) {
	label := func(s string) osvVuln { v := osvVuln{}; v.DatabaseSpecific.Severity = s; return v }
	if osvSeverity(label("HIGH")) != SeverityHigh {
		t.Error("HIGH -> high")
	}
	if osvSeverity(label("moderate")) != SeverityModerate {
		t.Error("moderate -> moderate")
	}
	if osvSeverity(osvVuln{}) != SeverityUnknown { // no label, no scorable CVSS
		t.Error("empty -> unknown")
	}

	var aliasVuln osvVuln
	_ = json.Unmarshal([]byte(`{"id":"x","aliases":["CVE-1","GHSA-z","OSV-2"]}`), &aliasVuln)
	if got := cveAliases(aliasVuln); !reflect.DeepEqual(got, []string{"CVE-1"}) {
		t.Errorf("cveAliases = %v", got)
	}

	var fixedVuln osvVuln
	_ = json.Unmarshal([]byte(`{"id":"x","affected":[
		{"package":{"name":"g:a"},"ranges":[{"events":[{"introduced":"1"},{"fixed":"2"}]}]},
		{"package":{"name":"other:x"},"ranges":[{"events":[{"fixed":"9"}]}]}]}`), &fixedVuln)
	if got := fixedVersionsOf(fixedVuln, coord("g:a:1.5")); !reflect.DeepEqual(got, []string{"2"}) {
		t.Errorf("fixedVersionsOf = %v (only the matching package's fix)", got)
	}
}
