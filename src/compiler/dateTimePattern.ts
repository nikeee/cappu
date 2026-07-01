// Validator for java.time.format.DateTimeFormatter pattern strings. Reports two
// things from a literal pattern: (1) unknown pattern letters, which throw
// IllegalArgumentException at runtime, and (2) the classic silent-bug footguns
// (Y vs y, D vs d, h vs H) that compile and run but produce wrong output.
// Mirrored by togo/internal/compiler/datetimepattern.go.

// The reserved/meaningful pattern letters (java.time.format.DateTimeFormatter).
// Any other ASCII letter throws "Unknown pattern letter".
const VALID = new Set("GuyDMLdQqYwWEecFaBhHkKmsSAnNVvzOXxZp".split(""));

export interface DateTimeFootgun {
  readonly letter: string;
  readonly meaning: string;
  readonly suggest: string;
}

export interface DateTimePatternReport {
  readonly invalidLetters: readonly string[];
  readonly footguns: readonly DateTimeFootgun[];
}

export function checkDateTimePattern(pattern: string): DateTimePatternReport {
  const invalid = new Set<string>();
  const present = new Set<string>();
  let i = 0;
  while (i < pattern.length) {
    const ch = pattern[i]!;
    if (ch === "'") {
      // A quoted literal runs to the next single quote ('' escapes a quote).
      i++;
      while (i < pattern.length && pattern[i] !== "'") i++;
      i++; // skip the closing quote
      continue;
    }
    if (/[a-zA-Z]/.test(ch)) {
      present.add(ch);
      if (!VALID.has(ch)) invalid.add(ch);
    }
    i++;
  }

  const footguns: DateTimeFootgun[] = [];
  // 'Y' (week-based-year) outside a week context almost always meant 'y'.
  if (present.has("Y") && !present.has("w") && !present.has("W")) {
    footguns.push({ letter: "Y", meaning: "week-based-year", suggest: "y" });
  }
  // 'D' (day-of-year) alongside a month almost always meant 'd' (day-of-month).
  if (present.has("D") && present.has("M")) {
    footguns.push({ letter: "D", meaning: "day-of-year", suggest: "d" });
  }
  // 'h' (clock-hour 1-12) with no am/pm marker almost always meant 'H' (0-23).
  if (present.has("h") && !present.has("a") && !present.has("B")) {
    footguns.push({ letter: "h", meaning: "clock-hour of am/pm (1-12)", suggest: "H" });
  }
  return { invalidLetters: [...invalid], footguns };
}
