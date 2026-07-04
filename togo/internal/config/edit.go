package config

// Surgical JSONC editing: change one value (or insert/remove one member) and
// leave every other byte - comments, indentation, trailing commas - untouched.
// Port of src/cli/jsoncEdit.ts (the TS reference); the two builds must produce
// byte-identical files for the same edit.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

type member struct {
	keyStart, keyEnd     int // opening quote / just past closing quote
	key                  string
	valueStart, valueEnd int
}

type objectSpan struct {
	open, close int // "{" / "}"
	members     []member
}

var errNotAnObject = errors.New("the config file does not contain an object")

// skipTrivia skips whitespace and JSONC comments starting at i.
func skipTrivia(text string, i int) int {
	for {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n') {
			i++
		}
		switch {
		case strings.HasPrefix(text[i:], "//"):
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return len(text)
			}
			i += 2 + end + 2
		default:
			return i
		}
	}
}

// skipString expects text[i] == '"' and returns the index just past the
// closing quote.
func skipString(text string, i int) (int, error) {
	i++
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1, nil
		default:
			i++
		}
	}
	return 0, errors.New("unterminated string in config file")
}

// skipValue expects i at the first character of a value and returns the index
// just past it.
func skipValue(text string, i int) (int, error) {
	c := text[i]
	if c == '"' {
		return skipString(text, i)
	}
	if c == '{' || c == '[' {
		closer := byte('}')
		if c == '[' {
			closer = ']'
		}
		depth := 0
		for i < len(text) {
			ch := text[i]
			if ch == '"' {
				next, err := skipString(text, i)
				if err != nil {
					return 0, err
				}
				i = next
				continue
			}
			if strings.HasPrefix(text[i:], "//") || strings.HasPrefix(text[i:], "/*") {
				i = skipTrivia(text, i)
				continue
			}
			switch ch {
			case c:
				depth++
			case closer:
				depth--
				if depth == 0 {
					return i + 1, nil
				}
			}
			i++
		}
		return 0, errors.New("unterminated value in config file")
	}
	// number / true / false / null
	for i < len(text) && !strings.ContainsRune(",}]\n\r\t ", rune(text[i])) {
		i++
	}
	return i, nil
}

// parseObject parses the object starting at open (must be '{').
func parseObject(text string, open int) (objectSpan, error) {
	var members []member
	i := skipTrivia(text, open+1)
	for i < len(text) && text[i] != '}' {
		if text[i] != '"' {
			return objectSpan{}, errors.New("expected a string key in config file")
		}
		keyStart := i
		keyEnd, err := skipString(text, i)
		if err != nil {
			return objectSpan{}, err
		}
		var key string
		if err := json.Unmarshal([]byte(text[keyStart:keyEnd]), &key); err != nil {
			return objectSpan{}, err
		}
		i = skipTrivia(text, keyEnd)
		if i >= len(text) || text[i] != ':' {
			return objectSpan{}, errors.New("expected ':' in config file")
		}
		valueStart := skipTrivia(text, i+1)
		valueEnd, err := skipValue(text, valueStart)
		if err != nil {
			return objectSpan{}, err
		}
		members = append(members, member{keyStart, keyEnd, key, valueStart, valueEnd})
		i = skipTrivia(text, valueEnd)
		if i < len(text) && text[i] == ',' {
			i = skipTrivia(text, i+1) // includes trailing comma
		}
	}
	if i >= len(text) {
		return objectSpan{}, errors.New("unterminated object in config file")
	}
	return objectSpan{open: open, close: i, members: members}, nil
}

func rootObject(text string) (objectSpan, error) {
	start := skipTrivia(text, 0)
	if start >= len(text) || text[start] != '{' {
		return objectSpan{}, errNotAnObject
	}
	return parseObject(text, start)
}

// lineIndent is the leading whitespace of the line containing index i.
func lineIndent(text string, i int) string {
	lineStart := strings.LastIndexByte(text[:i], '\n') + 1
	end := lineStart
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	return text[lineStart:end]
}

// indentUnit is one extra indentation level, inferred from a parent/member pair.
func indentUnit(parent, member string) string {
	if len(member) > len(parent) {
		return member[len(parent):]
	}
	return "  "
}

func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// renderNested renders path -> value as nested object members.
func renderNested(path []string, value string, multiline bool, indent, unit string) string {
	colon := ":"
	if multiline {
		colon = ": "
	}
	if len(path) == 1 {
		return q(path[0]) + colon + q(value)
	}
	inner := renderNested(path[1:], value, multiline, indent+unit, unit)
	if multiline {
		return q(path[0]) + colon + "{\n" + indent + unit + inner + "\n" + indent + "}"
	}
	return q(path[0]) + colon + "{" + inner + "}"
}

// walk descends path as deep as it exists. found is non-nil when the full
// path resolved; rest holds the unconsumed segments otherwise.
func walk(text string, path []string) (parent objectSpan, found *member, rest []string, err error) {
	parent, err = rootObject(text)
	if err != nil {
		return objectSpan{}, nil, nil, err
	}
	for depth := range path {
		var m *member
		for idx := range parent.members {
			if parent.members[idx].key == path[depth] {
				m = &parent.members[idx]
				break
			}
		}
		if m == nil {
			return parent, nil, path[depth:], nil
		}
		if depth == len(path)-1 {
			return parent, m, nil, nil
		}
		if text[m.valueStart] != '{' {
			return parent, nil, path[depth:], nil
		}
		parent, err = parseObject(text, m.valueStart)
		if err != nil {
			return objectSpan{}, nil, nil, err
		}
	}
	return parent, nil, nil, nil
}

// setValue sets the string value at path, replacing an existing value in
// place or inserting new members (creating intermediate sections) at the end
// of the deepest existing object.
func setValue(text string, path []string, value string) (string, error) {
	parent, found, rest, err := walk(text, path)
	if err != nil {
		return "", err
	}
	if found != nil {
		return text[:found.valueStart] + q(value) + text[found.valueEnd:], nil
	}
	objectText := text[parent.open : parent.close+1]
	multiline := strings.Contains(objectText, "\n")
	if len(parent.members) > 0 {
		last := parent.members[len(parent.members)-1]
		indent, unit := "", ""
		if multiline {
			indent = lineIndent(text, last.keyStart)
			unit = indentUnit(lineIndent(text, parent.open), indent)
		}
		memberText := renderNested(rest, value, multiline, indent, unit)
		// Respect an existing trailing comma; otherwise add the separator.
		afterLast := skipTrivia(text, last.valueEnd)
		hasTrailingComma := afterLast < len(text) && text[afterLast] == ','
		insertAt := last.valueEnd
		separator := ","
		if hasTrailingComma {
			insertAt = afterLast + 1
			separator = ""
		}
		// ponytail: new members use the house style (": " when multiline, ":"
		// when compact) rather than sniffing the file's colon spacing.
		glue := separator
		if multiline {
			glue = separator + "\n" + indent
		}
		return text[:insertAt] + glue + memberText + text[insertAt:], nil
	}
	// Empty object: rewrite its span.
	grow := multiline || strings.Contains(text, "\n")
	indent, unit := "", ""
	if grow {
		indent = lineIndent(text, parent.open)
		unit = "  "
	}
	memberText := renderNested(rest, value, grow, indent+unit, unit)
	replacement := "{" + memberText + "}"
	if grow {
		replacement = "{\n" + indent + unit + memberText + "\n" + indent + "}"
	}
	return text[:parent.open] + replacement + text[parent.close+1:], nil
}

var (
	wsOnly          = regexp.MustCompile(`^[ \t]*$`)
	wsOrLineComment = regexp.MustCompile(`^[ \t]*(//.*)?$`)
)

// removeKey removes the member at path. Absent key (or section) is a no-op.
func removeKey(text string, path []string) (string, bool, error) {
	parent, found, _, err := walk(text, path)
	if err != nil {
		return "", false, err
	}
	if found == nil {
		return text, false, nil
	}

	// The member's span: its whole line when it sits alone on one (including a
	// trailing comma and a trailing comment), so comments on OTHER members are
	// never touched.
	lineStart := strings.LastIndexByte(text[:found.keyStart], '\n') + 1
	ownsLine := wsOnly.MatchString(text[lineStart:found.keyStart])
	start := found.keyStart
	if ownsLine {
		start = lineStart
	}
	afterValue := skipTrivia(text, found.valueEnd)
	hasComma := afterValue < len(text) && text[afterValue] == ','
	end := found.valueEnd
	if hasComma {
		end = afterValue + 1
	}
	if ownsLine {
		// Swallow the rest of the line (whitespace or the member's own
		// trailing comment) and the newline.
		if nl := strings.IndexByte(text[end:], '\n'); nl >= 0 && wsOrLineComment.MatchString(text[end:end+nl]) {
			end += nl + 1
		}
	}
	out := text[:start] + text[end:]

	// A last member without its own trailing comma leaves the previous
	// member's separator dangling; drop that single comma character.
	index := -1
	for idx := range parent.members {
		if parent.members[idx].keyStart == found.keyStart {
			index = idx
			break
		}
	}
	if !hasComma && index == len(parent.members)-1 && index > 0 {
		previous := parent.members[index-1]
		afterPrevious := skipTrivia(text, previous.valueEnd)
		if afterPrevious < len(text) && text[afterPrevious] == ',' {
			out = out[:afterPrevious] + out[afterPrevious+1:]
		}
	}
	return out, true, nil
}

// SetStringField sets a top-level string field in the JSONC config text,
// preserving comments and formatting (only the value's span changes).
func SetStringField(text []byte, key, value string) ([]byte, error) {
	out, err := setValue(string(text), []string{key}, value)
	return []byte(out), err
}

// SetDependency inserts or overwrites dependencies.<configuration>.<key> in
// the JSONC config text, preserving comments. Port of addDependencyToJsonc.
func SetDependency(text []byte, configuration, key, version string) ([]byte, error) {
	out, err := setValue(string(text), []string{"dependencies", configuration, key}, version)
	return []byte(out), err
}

// HasDependency reports whether dependencies.<configuration>.<key> exists.
func HasDependency(text []byte, configuration, key string) bool {
	_, found, _, err := walk(string(text), []string{"dependencies", configuration, key})
	return err == nil && found != nil
}

// RemoveDependency deletes dependencies.<configuration>.<key> from the JSONC
// config text, preserving comments. removed reports whether the key was
// present. Port of removeDependencyFromJsonc.
func RemoveDependency(text []byte, configuration, key string) ([]byte, bool, error) {
	out, removed, err := removeKey(string(text), []string{"dependencies", configuration, key})
	if err != nil {
		return text, false, err
	}
	return []byte(out), removed, nil
}
