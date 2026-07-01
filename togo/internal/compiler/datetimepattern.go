// Port of src/compiler/dateTimePattern.ts
//
// Validator for java.time.format.DateTimeFormatter pattern strings: unknown
// pattern letters (which throw IllegalArgumentException) plus the classic
// silent-bug footguns (Y vs y, D vs d, h vs H).

package compiler

// The reserved/meaningful pattern letters. Any other ASCII letter throws.
var dateTimeValid = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range "GuyDMLdQqYwWEecFaBhHkKmsSAnNVvzOXxZp" {
		m[r] = true
	}
	return m
}()

// DateTimeFootgun is a valid-but-suspicious pattern letter.
type DateTimeFootgun struct {
	Letter  string
	Meaning string
	Suggest string
}

// DateTimePatternReport is the result of CheckDateTimePattern.
type DateTimePatternReport struct {
	InvalidLetters []string
	Footguns       []DateTimeFootgun
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// CheckDateTimePattern reports unknown letters and Y/D/h footguns in a pattern.
func CheckDateTimePattern(pattern string) DateTimePatternReport {
	invalid := []string{}
	invalidSeen := map[byte]bool{}
	present := map[byte]bool{}
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		if ch == '\'' {
			// A quoted literal runs to the next single quote ('' escapes a quote).
			i++
			for i < len(pattern) && pattern[i] != '\'' {
				i++
			}
			i++ // skip the closing quote
			continue
		}
		if isASCIILetter(ch) {
			present[ch] = true
			if !dateTimeValid[rune(ch)] && !invalidSeen[ch] {
				invalidSeen[ch] = true
				invalid = append(invalid, string(ch))
			}
		}
		i++
	}

	footguns := []DateTimeFootgun{}
	if present['Y'] && !present['w'] && !present['W'] {
		footguns = append(footguns, DateTimeFootgun{"Y", "week-based-year", "y"})
	}
	if present['D'] && present['M'] {
		footguns = append(footguns, DateTimeFootgun{"D", "day-of-year", "d"})
	}
	if present['h'] && !present['a'] && !present['B'] {
		footguns = append(footguns, DateTimeFootgun{"h", "clock-hour of am/pm (1-12)", "H"})
	}
	return DateTimePatternReport{InvalidLetters: invalid, Footguns: footguns}
}
