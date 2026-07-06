package packages

import (
	"reflect"
	"testing"
)

func mustNormalize(t *testing.T, name, url, want string) {
	t.Helper()
	got, ok := NormalizeLicense(name, url)
	if !ok || string(got) != want {
		t.Errorf("NormalizeLicense(%q, %q) = (%q, %v), want %q", name, url, got, ok, want)
	}
}

// Port of src/packages/license.test.ts.
func TestNormalizeVariants(t *testing.T) {
	for _, name := range []string{
		"Apache-2.0",
		"Apache License, Version 2.0",
		"The Apache Software License, Version 2.0",
		"Apache Software License - Version 2.0",
	} {
		mustNormalize(t, name, "", "Apache-2.0")
	}
	mustNormalize(t, "MIT", "", "MIT")
	mustNormalize(t, "The MIT License", "", "MIT")
	mustNormalize(t, "Eclipse Public License - v 1.0", "", "EPL-1.0")
	mustNormalize(t, "EPL 2.0", "", "EPL-2.0")
	mustNormalize(t, "New BSD License", "", "BSD-3-Clause")
}

func TestNormalizeCommonPackageNames(t *testing.T) {
	expected := map[string]string{
		"Apache License, Version 2.0":              "Apache-2.0",
		"Apache Software License - Version 2.0":    "Apache-2.0",
		"Apache-2.0":                               "Apache-2.0",
		"The Apache License, Version 2.0":          "Apache-2.0",
		"The Apache Software License, Version 2.0": "Apache-2.0",
		"BSD License 3":                            "BSD-3-Clause",
		"BSD-2-Clause":                             "BSD-2-Clause",
		"BSD-3-Clause":                             "BSD-3-Clause",
		"CDDL + GPLv2 with classpath exception":    "CDDL-1.1 OR GPL-2.0-with-classpath-exception",
		"GPL2 w/ CPE":                              "GPL-2.0-with-classpath-exception",
		"Eclipse Public License - v 1.0":           "EPL-1.0",
		"Eclipse Public License 1.0":               "EPL-1.0",
		"EPL 1.0":                                  "EPL-1.0",
		"Eclipse Public License - Version 2.0":     "EPL-2.0",
		"Eclipse Public License v2.0":              "EPL-2.0",
		"EPL 2.0":                                  "EPL-2.0",
		"GNU Lesser General Public License":        "LGPL-2.1",
		"MIT":                                      "MIT",
		"MIT License":                              "MIT",
		"The MIT License":                          "MIT",
		"MPL 2.0":                                  "MPL-2.0",
	}
	for name, spdx := range expected {
		mustNormalize(t, name, "", spdx)
	}
}

func TestNormalizeFromURL(t *testing.T) {
	mustNormalize(t, "Custom Vendor Terms", "https://www.apache.org/licenses/LICENSE-2.0.txt", "Apache-2.0")
	mustNormalize(t, "LGPL", "http://www.gnu.org/licenses/old-licenses/lgpl-2.1.html", "LGPL-2.1")
	mustNormalize(t, "EPL", "https://www.eclipse.org/legal/epl-2.0/", "EPL-2.0")
	// the name still wins when it maps, whatever the url
	mustNormalize(t, "MIT License", "https://example.com/whatever", "MIT")
}

// The exact <license><name> strings that installing jmh and the jakarta xml
// bind stack surfaced as "no SPDX mapping" during the Maven->cappu migration.
func TestNormalizeMigrationLicenses(t *testing.T) {
	mustNormalize(t, "GNU General Public License (GPL), version 2, with the Classpath exception", "", "GPL-2.0-with-classpath-exception")
	mustNormalize(t, "Eclipse Distribution License - v 1.0", "", "BSD-3-Clause")
	mustNormalize(t, "EDL 1.0", "", "BSD-3-Clause")
	mustNormalize(t, "EDL", "http://www.eclipse.org/org/documents/edl-v10.php", "BSD-3-Clause")
}

func TestNormalizeUnrecognized(t *testing.T) {
	for _, c := range []struct{ name, url string }{
		{"Public Domain", ""},
		{"Weird Custom License 1.3", ""},
		{"Custom", "https://example.com/license.txt"},
	} {
		if _, ok := NormalizeLicense(c.name, c.url); ok {
			t.Errorf("NormalizeLicense(%q, %q) should have no mapping", c.name, c.url)
		}
	}
}

func TestNormalizeLicensesDedupes(t *testing.T) {
	got := NormalizeLicenses([]License{
		{Name: "Apache-2.0"},
		{Name: "The Apache Software License, Version 2.0"}, // same id, deduped
		{Name: "MIT License"},
		{Name: "Public Domain"}, // unmapped, dropped
	})
	want := []SpdxID{"Apache-2.0", "MIT"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeLicenses = %v, want %v", got, want)
	}
}
