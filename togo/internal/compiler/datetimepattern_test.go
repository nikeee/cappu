// Port of src/compiler/dateTimePattern.test.ts
package compiler

import (
	"slices"
	"testing"
)

func footgunLetters(r DateTimePatternReport) []string {
	var out []string
	for _, f := range r.Footguns {
		out = append(out, f.Letter)
	}
	return out
}

func TestDateTimeValid(t *testing.T) {
	r := CheckDateTimePattern("yyyy-MM-dd HH:mm:ss")
	if len(r.InvalidLetters) != 0 || len(r.Footguns) != 0 {
		t.Errorf("got %+v, want clean", r)
	}
}

func TestDateTimeUnknownLetter(t *testing.T) {
	if !slices.Contains(CheckDateTimePattern("yyyy-jj").InvalidLetters, "j") {
		t.Error("want 'j' flagged")
	}
}

func TestDateTimeQuoted(t *testing.T) {
	if got := CheckDateTimePattern("yyyy 'at' HH'h'").InvalidLetters; len(got) != 0 {
		t.Errorf("quoted letters flagged: %v", got)
	}
}

func TestDateTimeFootguns(t *testing.T) {
	if !slices.Contains(footgunLetters(CheckDateTimePattern("YYYY-MM-dd")), "Y") {
		t.Error("want Y footgun")
	}
	if got := CheckDateTimePattern("YYYY-'W'ww").Footguns; len(got) != 0 {
		t.Errorf("Y with week field flagged: %v", got)
	}
	if !slices.Contains(footgunLetters(CheckDateTimePattern("yyyy-MM-DD")), "D") {
		t.Error("want D footgun")
	}
	if !slices.Contains(footgunLetters(CheckDateTimePattern("hh:mm")), "h") {
		t.Error("want h footgun")
	}
	if got := CheckDateTimePattern("hh:mm a").Footguns; len(got) != 0 {
		t.Errorf("h with am/pm flagged: %v", got)
	}
}
