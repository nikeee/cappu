package packages

import (
	"math/big"
	"regexp"
	"strings"
)

// Maven version-range support. Port of src/packages/versionRange.ts.
//
// cappu otherwise treats a declared version as an exact coordinate; a Maven
// range (`[1.0,2.0)`) - in cappu.json or a real-world transitive POM - must
// first be resolved to a concrete published version. This file provides the
// ordering (a subset of Maven's ComparableVersion), the range parser,
// membership, and the "highest published match" selector.
//
// Scope: bracket/paren ranges, comma-joined sets, and the RELEASE/LATEST
// tokens. A bare version keeps its exact-pin meaning (ParseVersionSpec returns
// ok=false for it) and is NOT reinterpreted as a Maven soft requirement.

// Known qualifier ranks (lower sorts earlier). "" is the release itself and
// outranks every pre-release qualifier; "sp" is >= release.
// ponytail: Maven's full alias table is reproduced only for the common aliases
// below; an unknown qualifier sorts after the release and lexically among its
// peers - upgrade the table if a real dependency's ordering needs it.
var qualifierRank = map[string]int{
	"alpha":     -6,
	"a":         -6,
	"beta":      -5,
	"b":         -5,
	"milestone": -4,
	"m":         -4,
	"rc":        -3,
	"cr":        -3,
	"snapshot":  -2,
	"":          0, // the release
	"ga":        0,
	"final":     0,
	"release":   0,
	"sp":        1,
}

type segment struct {
	num  *big.Int // nil for a qualifier segment
	qual string
}

var (
	splitRe = regexp.MustCompile(`[.\-_+]`)
	pieceRe = regexp.MustCompile(`\d+|[a-z]+`)
	digitRe = regexp.MustCompile(`^\d+$`)
)

// segmentsOf splits into numeric and qualifier segments. Maven separates on `.`
// and `-`, and also at a digit/letter transition ("1alpha" -> "1", "alpha").
func segmentsOf(version string) []segment {
	var segments []segment
	for _, raw := range splitRe.Split(strings.ToLower(version), -1) {
		if raw == "" {
			continue
		}
		for _, piece := range pieceRe.FindAllString(raw, -1) {
			if digitRe.MatchString(piece) {
				n := new(big.Int)
				n.SetString(piece, 10)
				segments = append(segments, segment{num: n})
			} else {
				segments = append(segments, segment{qual: piece})
			}
		}
	}
	return segments
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// compareQualifier compares two qualifiers by known rank, then lexically; a
// known qualifier always sorts before an unknown one.
func compareQualifier(aq, bq string) int {
	ar, aok := qualifierRank[aq]
	br, bok := qualifierRank[bq]
	switch {
	case aok && bok:
		return sign(ar - br)
	case aok:
		return -1
	case bok:
		return 1
	}
	return strings.Compare(aq, bq)
}

// compareToMissing compares a present segment against a missing one: a number
// compares against 0 (so 1.0 == 1.0.0), a qualifier against the release.
func compareToMissing(s segment) int {
	if s.num != nil {
		return s.num.Sign()
	}
	return compareQualifier(s.qual, "")
}

func compareSegment(a, b *segment) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -compareToMissing(*b)
	case b == nil:
		return compareToMissing(*a)
	}
	if a.num != nil && b.num != nil {
		return a.num.Cmp(b.num)
	}
	// A number always outranks a qualifier (Maven: 1.1 > 1.1-alpha).
	if a.num != nil {
		return 1
	}
	if b.num != nil {
		return -1
	}
	return compareQualifier(a.qual, b.qual)
}

// CompareVersions gives Maven-style ordering: negative if a<b, positive if a>b.
func CompareVersions(a, b string) int {
	as := segmentsOf(a)
	bs := segmentsOf(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	at := func(s []segment, i int) *segment {
		if i < len(s) {
			return &s[i]
		}
		return nil
	}
	for i := 0; i < n; i++ {
		if c := compareSegment(at(as, i), at(bs, i)); c != 0 {
			return c
		}
	}
	return 0
}

type restriction struct {
	lower          string
	lowerInclusive bool
	upper          string
	upperInclusive bool
	hasLower       bool
	hasUpper       bool
}

// VersionSpec is a parsed Maven version range or newest-wins token.
type VersionSpec struct {
	// Newest is set for RELEASE/LATEST: pick the highest published version.
	Newest bool
	// Restrictions are OR-joined; a version satisfies the spec if it satisfies any.
	restrictions []restriction
}

func parseRestriction(text string) (restriction, bool) {
	lowerInclusive := strings.HasPrefix(text, "[")
	upperInclusive := strings.HasSuffix(text, "]")
	open := strings.HasPrefix(text, "[") || strings.HasPrefix(text, "(")
	closed := strings.HasSuffix(text, "]") || strings.HasSuffix(text, ")")
	if !open || !closed {
		return restriction{}, false
	}
	inner := text[1 : len(text)-1]
	if !strings.Contains(inner, ",") {
		// [1.5] - a single hard version
		if !lowerInclusive || !upperInclusive || inner == "" {
			return restriction{}, false
		}
		return restriction{
			lower: inner, lowerInclusive: true, hasLower: true,
			upper: inner, upperInclusive: true, hasUpper: true,
		}, true
	}
	comma := strings.Index(inner, ",")
	lower := strings.TrimSpace(inner[:comma])
	upper := strings.TrimSpace(inner[comma+1:])
	return restriction{
		lower: lower, lowerInclusive: lowerInclusive, hasLower: lower != "",
		upper: upper, upperInclusive: upperInclusive, hasUpper: upper != "",
	}, true
}

// ParseVersionSpec parses a Maven version spec. ok is false when spec is a plain
// exact version (the caller then treats it as an exact coordinate, as before).
func ParseVersionSpec(spec string) (VersionSpec, bool) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "RELEASE" || trimmed == "LATEST" {
		return VersionSpec{Newest: true}, true
	}
	if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "(") {
		return VersionSpec{}, false
	}
	var restrictions []restriction
	depth := 0
	start := 0
	for i := 0; i <= len(trimmed); i++ {
		var ch byte
		if i < len(trimmed) {
			ch = trimmed[i]
		}
		switch ch {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		}
		if i == len(trimmed) || (ch == ',' && depth == 0) {
			part := strings.TrimSpace(trimmed[start:i])
			if part != "" {
				r, ok := parseRestriction(part)
				if !ok {
					return VersionSpec{}, false // malformed: fall back to exact
				}
				restrictions = append(restrictions, r)
			}
			start = i + 1
		}
	}
	if len(restrictions) == 0 {
		return VersionSpec{}, false
	}
	return VersionSpec{restrictions: restrictions}, true
}

func withinRestriction(r restriction, version string) bool {
	if r.hasLower {
		c := CompareVersions(version, r.lower)
		if c < 0 || (c == 0 && !r.lowerInclusive) {
			return false
		}
	}
	if r.hasUpper {
		c := CompareVersions(version, r.upper)
		if c > 0 || (c == 0 && !r.upperInclusive) {
			return false
		}
	}
	return true
}

// Satisfies reports whether version satisfies spec (RELEASE/LATEST accept any).
func Satisfies(spec VersionSpec, version string) bool {
	if spec.Newest {
		return true
	}
	for _, r := range spec.restrictions {
		if withinRestriction(r, version) {
			return true
		}
	}
	return false
}

// SelectVersion returns the highest published version satisfying spec (Maven
// picks the highest in range; RELEASE/LATEST pick the newest), or "" if none.
func SelectVersion(spec VersionSpec, published []string) string {
	best := ""
	for _, version := range published {
		if !Satisfies(spec, version) {
			continue
		}
		if best == "" || CompareVersions(version, best) > 0 {
			best = version
		}
	}
	return best
}
