package packages

import (
	"regexp"
	"strings"
)

// Package licenses. Maven's <license><name> is free text, not an SPDX
// identifier (the same license shows up as "Apache-2.0", "Apache License,
// Version 2.0", ...), so we keep the raw declaration and offer a best-effort
// SPDX normalization beside it. Port of src/packages/license.ts.

// SpdxID is a canonical SPDX license id ("Apache-2.0"), as opposed to a raw POM name.
type SpdxID string

// License is one license exactly as a POM's <licenses> declares it (raw, not SPDX).
type License struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeKey is a loose key for matching: lowercased, every run of
// non-alphanumerics folded to a single space. Collapses spelling/spacing/
// punctuation variants so one table entry covers "Apache-2.0", "Apache 2.0",
// "Apache License, Version 2.0".
func normalizeKey(name string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(name), " "))
}

// spdxAliases maps normalizeKey(raw) -> SPDX id (or expression for dual
// licenses). Seeded from the declared names of the most-depended-on Maven
// Central packages; add new variants as encountered.
var spdxAliases = map[string]string{
	"apache 2 0":                              "Apache-2.0",
	"apache license 2 0":                      "Apache-2.0",
	"apache license version 2 0":              "Apache-2.0",
	"apache software license version 2 0":     "Apache-2.0",
	"the apache license version 2 0":          "Apache-2.0",
	"the apache software license version 2 0": "Apache-2.0",
	"bsd 2 clause":                            "BSD-2-Clause",
	"bsd 3 clause":                            "BSD-3-Clause",
	"bsd license 3":                           "BSD-3-Clause",
	"new bsd license":                         "BSD-3-Clause", // common name for 3-clause BSD
	"the new bsd license":                     "BSD-3-Clause",
	"cddl gplv2 with classpath exception":     "CDDL-1.1 OR GPL-2.0-with-classpath-exception",
	"gpl2 w cpe":                              "GPL-2.0-with-classpath-exception",
	"gnu general public license gpl version 2 with the classpath exception": "GPL-2.0-with-classpath-exception",
	// The Eclipse Distribution License 1.0 is textually BSD-3-Clause (no distinct SPDX id).
	"eclipse distribution license v 1 0": "BSD-3-Clause",
	"edl 1 0":                            "BSD-3-Clause",
	"eclipse public license 1 0":         "EPL-1.0",
	"eclipse public license v 1 0":       "EPL-1.0",
	"epl 1 0":                            "EPL-1.0",
	"eclipse public license v2 0":        "EPL-2.0",
	"eclipse public license version 2 0": "EPL-2.0",
	"epl 2 0":                            "EPL-2.0",
	"gnu lesser general public license":  "LGPL-2.1",
	"gnu lesser public license":          "LGPL-2.1",
	"mit":                                "MIT",
	"mit license":                        "MIT",
	"the mit license":                    "MIT",
	"mozilla public license version 1 0": "MPL-1.0",
	"mpl 2 0":                            "MPL-2.0",
}

// spdxURLPatterns maps a canonical license-deed URL substring to its SPDX id -
// the fallback when the name does not match. Matched against the url lowercased
// with the scheme, any "www." and trailing slashes stripped.
var spdxURLPatterns = []struct{ needle, spdx string }{
	{"oss.oracle.com/licenses/cddl+gpl", "CDDL-1.1 OR GPL-2.0-with-classpath-exception"},
	{"classpath/license", "GPL-2.0-with-classpath-exception"},
	{"apache.org/licenses/license-2.0", "Apache-2.0"},
	{"opensource.org/licenses/apache-2.0", "Apache-2.0"},
	{"opensource.org/licenses/bsd-3-clause", "BSD-3-Clause"},
	{"opensource.org/licenses/bsd-2-clause", "BSD-2-Clause"},
	{"eclipse.org/legal/epl-2.0", "EPL-2.0"},
	{"eclipse.org/legal/epl-v20", "EPL-2.0"},
	{"eclipse.org/legal/epl-v10", "EPL-1.0"},
	{"documents/edl-v10", "BSD-3-Clause"}, // Eclipse Distribution License 1.0 == BSD-3-Clause
	{"eclipse.org/legal/epl-1.0", "EPL-1.0"},
	{"opensource.org/licenses/eclipse-1.0", "EPL-1.0"},
	{"mozilla.org/en-us/mpl/2.0", "MPL-2.0"},
	{"mozilla.org/mpl/2.0", "MPL-2.0"},
	{"gnu.org/licenses/old-licenses/lgpl-2.1", "LGPL-2.1"},
	{"opensource.org/licenses/mit", "MIT"},
}

var (
	schemePrefix    = regexp.MustCompile(`^https?://`)
	wwwPrefix       = regexp.MustCompile(`^www\.`)
	trailingSlashes = regexp.MustCompile(`/+$`)
)

func spdxFromURL(url string) (SpdxID, bool) {
	normalized := strings.ToLower(url)
	normalized = schemePrefix.ReplaceAllString(normalized, "")
	normalized = wwwPrefix.ReplaceAllString(normalized, "")
	normalized = trailingSlashes.ReplaceAllString(normalized, "")
	for _, p := range spdxURLPatterns {
		if strings.Contains(normalized, p.needle) {
			return SpdxID(p.spdx), true
		}
	}
	return "", false
}

// NormalizeLicense returns a best-effort SPDX id for a license: the name first,
// then (when the name does not match) a known license url. ok is false when
// neither is recognized.
func NormalizeLicense(name, url string) (SpdxID, bool) {
	if spdx, ok := spdxAliases[normalizeKey(name)]; ok {
		return SpdxID(spdx), true
	}
	if url != "" {
		return spdxFromURL(url)
	}
	return "", false
}

// NormalizeLicenses returns the deduped SPDX ids the licenses map to (dropping
// the ones with no mapping), preserving first-seen order.
func NormalizeLicenses(licenses []License) []SpdxID {
	var ids []SpdxID
	seen := make(map[SpdxID]struct{})
	for _, license := range licenses {
		spdx, ok := NormalizeLicense(license.Name, license.URL)
		if !ok {
			continue
		}
		if _, dup := seen[spdx]; dup {
			continue
		}
		seen[spdx] = struct{}{}
		ids = append(ids, spdx)
	}
	return ids
}
