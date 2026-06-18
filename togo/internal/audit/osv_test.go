package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

func coord(spec string) packages.Coordinates {
	p := strings.Split(spec, ":")
	return packages.NewCoordinates(p[0], p[1], p[2])
}

func TestOsvFindVulnerabilities(t *testing.T) {
	fetch := func(url string, body any) ([]byte, error) {
		switch {
		case strings.HasSuffix(url, "/v1/querybatch"):
			// results line up with the queries: a is vulnerable, b is clean
			return []byte(`{"results":[{"vulns":[{"id":"GHSA-xxxx"}]},{}]}`), nil
		case strings.HasSuffix(url, "/v1/vulns/GHSA-xxxx"):
			return []byte(`{
				"id":"GHSA-xxxx",
				"summary":"a nasty bug",
				"aliases":["CVE-2021-1","GHSA-yyyy"],
				"database_specific":{"severity":"HIGH"},
				"affected":[{"package":{"name":"org.a:a"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"1.1"}]}]}]
			}`), nil
		}
		t.Fatalf("unexpected url %q", url)
		return nil, nil
	}
	source := NewOsvSource(fetch)
	got, err := source.FindVulnerabilities([]packages.Coordinates{coord("org.a:a:1.0"), coord("org.b:b:2.0")})
	if err != nil {
		t.Fatal(err)
	}
	advisories := got["org.a:a:1.0"]
	if len(advisories) != 1 {
		t.Fatalf("got %d advisories for a, want 1", len(advisories))
	}
	a := advisories[0]
	if a.ID != "GHSA-xxxx" || a.Severity != SeverityHigh {
		t.Errorf("advisory = %+v", a)
	}
	if !reflect.DeepEqual(a.Aliases, []string{"CVE-2021-1"}) { // only CVE aliases kept
		t.Errorf("aliases = %v", a.Aliases)
	}
	if !reflect.DeepEqual(a.FixedVersions, []string{"1.1"}) {
		t.Errorf("fixedVersions = %v", a.FixedVersions)
	}
	if _, ok := got["org.b:b:2.0"]; ok {
		t.Error("clean package b should have no entry")
	}
}

func TestOsvSeverityFromCVSS(t *testing.T) {
	cases := []struct {
		score string
		want  Severity
	}{
		{"9.8", SeverityCritical},
		{"7.5", SeverityHigh},
		{"5.0", SeverityModerate},
		{"2.0", SeverityLow},
	}
	for _, c := range cases {
		v := osvVuln{}
		v.Severity = append(v.Severity, struct {
			Type  string `json:"type"`
			Score string `json:"score"`
		}{"CVSS_V3", c.score})
		if got := osvSeverity(v); got != c.want {
			t.Errorf("osvSeverity(score %s) = %s, want %s", c.score, got, c.want)
		}
	}
	// GHSA label wins over any CVSS score
	v := osvVuln{}
	v.DatabaseSpecific.Severity = "critical"
	if got := osvSeverity(v); got != SeverityCritical {
		t.Errorf("GHSA label = %s, want critical", got)
	}
	// nothing usable -> unknown
	if got := osvSeverity(osvVuln{}); got != SeverityUnknown {
		t.Errorf("empty = %s, want unknown", got)
	}
}

func TestCachedFetchJSONCachesVulnDetails(t *testing.T) {
	t.Setenv("CAPPU_PACKAGE_STORE", t.TempDir())
	calls := 0
	inner := func(url string, body any) ([]byte, error) {
		calls++
		return []byte(`{"id":"GHSA-z"}`), nil
	}
	cached := CachedFetchJSON(inner)
	url := "https://api.osv.dev/v1/vulns/GHSA-z"
	if _, err := cached(url, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cached(url, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("inner called %d times, want 1 (second served from cache)", calls)
	}
	// querybatch (POST) is never cached
	calls = 0
	post := "https://api.osv.dev/v1/querybatch"
	_, _ = cached(post, map[string]any{"queries": []any{}})
	_, _ = cached(post, map[string]any{"queries": []any{}})
	if calls != 2 {
		t.Errorf("querybatch cached unexpectedly: %d calls, want 2", calls)
	}
}
