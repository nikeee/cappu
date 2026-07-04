// Surgical JSONC editing: change one value (or insert/remove one member) and
// leave every other byte - comments, indentation, trailing commas - untouched.
// Replaces the comment-json parse/re-stringify round trip, which reformatted
// the whole file. The Go build (togo/internal/config/edit.go) implements the
// same algorithm; the two must produce byte-identical files for the same edit.

interface Member {
  /** Index of the opening quote of the key. */
  keyStart: number;
  /** Index just past the closing quote of the key. */
  keyEnd: number;
  key: string;
  valueStart: number;
  /** Index just past the value. */
  valueEnd: number;
}

interface ObjectSpan {
  /** Index of "{". */
  open: number;
  /** Index of "}". */
  close: number;
  members: Member[];
}

/** Skip whitespace and JSONC comments starting at i. */
function skipTrivia(text: string, i: number): number {
  for (;;) {
    while (i < text.length && /\s/.test(text[i])) i++;
    if (text.startsWith("//", i)) {
      while (i < text.length && text[i] !== "\n") i++;
    } else if (text.startsWith("/*", i)) {
      const end = text.indexOf("*/", i + 2);
      i = end < 0 ? text.length : end + 2;
    } else {
      return i;
    }
  }
}

/** i must be at a `"`; returns the index just past the closing quote. */
function skipString(text: string, i: number): number {
  i++;
  while (i < text.length) {
    if (text[i] === "\\") i += 2;
    else if (text[i] === '"') return i + 1;
    else i++;
  }
  throw new Error("unterminated string in config file");
}

/** i must be at the first character of a value; returns the index just past it. */
function skipValue(text: string, i: number): number {
  const c = text[i];
  if (c === '"') return skipString(text, i);
  if (c === "{" || c === "[") {
    const closer = c === "{" ? "}" : "]";
    let depth = 0;
    while (i < text.length) {
      const ch = text[i];
      if (ch === '"') {
        i = skipString(text, i);
        continue;
      }
      if (text.startsWith("//", i) || text.startsWith("/*", i)) {
        i = skipTrivia(text, i);
        continue;
      }
      if (ch === c) depth++;
      else if (ch === closer && --depth === 0) return i + 1;
      i++;
    }
    throw new Error("unterminated value in config file");
  }
  // number / true / false / null
  while (i < text.length && !",}]\n\r\t ".includes(text[i])) i++;
  return i;
}

/** Parse the object starting at `open` (must be `{`). */
function parseObject(text: string, open: number): ObjectSpan {
  const members: Member[] = [];
  let i = skipTrivia(text, open + 1);
  while (i < text.length && text[i] !== "}") {
    if (text[i] !== '"') throw new Error("expected a string key in config file");
    const keyStart = i;
    const keyEnd = skipString(text, i);
    const key = JSON.parse(text.slice(keyStart, keyEnd)) as string;
    i = skipTrivia(text, keyEnd);
    if (text[i] !== ":") throw new Error("expected ':' in config file");
    const valueStart = skipTrivia(text, i + 1);
    const valueEnd = skipValue(text, valueStart);
    members.push({ keyStart, keyEnd, key, valueStart, valueEnd });
    i = skipTrivia(text, valueEnd);
    if (text[i] === ",") i = skipTrivia(text, i + 1); // includes trailing comma
  }
  if (i >= text.length) throw new Error("unterminated object in config file");
  return { open, close: i, members };
}

function rootObject(text: string): ObjectSpan {
  const start = skipTrivia(text, 0);
  if (text[start] !== "{") throw new Error("the config file does not contain an object");
  return parseObject(text, start);
}

/** The leading whitespace of the line containing index i. */
function lineIndent(text: string, i: number): string {
  const lineStart = text.lastIndexOf("\n", i - 1) + 1;
  let end = lineStart;
  while (end < text.length && (text[end] === " " || text[end] === "\t")) end++;
  return text.slice(lineStart, end);
}

/** One extra indentation level, inferred from an existing parent/member pair. */
function indentUnit(parent: string, member: string): string {
  return member.length > parent.length ? member.slice(parent.length) : "  ";
}

const q = (s: string): string => JSON.stringify(s);

/** Render `path -> value` as nested object members. */
function renderNested(
  path: readonly string[],
  value: string,
  multiline: boolean,
  indent: string,
  unit: string,
): string {
  const colon = multiline ? ": " : ":";
  if (path.length === 1) return `${q(path[0])}${colon}${q(value)}`;
  const inner = renderNested(path.slice(1), value, multiline, indent + unit, unit);
  return multiline
    ? `${q(path[0])}${colon}{\n${indent + unit}${inner}\n${indent}}`
    : `${q(path[0])}${colon}{${inner}}`;
}

/**
 * Walk path as deep as it exists. Returns the deepest existing object, the
 * member matched at the final consumed segment (if the full path resolved),
 * and the unconsumed segments.
 */
function walk(
  text: string,
  path: readonly string[],
): { parent: ObjectSpan; found?: Member; rest: readonly string[] } {
  let parent = rootObject(text);
  for (let depth = 0; depth < path.length; depth++) {
    const member = parent.members.find(m => m.key === path[depth]);
    if (!member) return { parent, rest: path.slice(depth) };
    if (depth === path.length - 1) return { parent, found: member, rest: [] };
    if (text[member.valueStart] !== "{") return { parent, rest: path.slice(depth) };
    parent = parseObject(text, member.valueStart);
  }
  return { parent, rest: [] };
}

/**
 * Set the string value at path, replacing an existing value in place or
 * inserting new members (creating intermediate sections) at the end of the
 * deepest existing object.
 */
export function setJsoncValue(text: string, path: readonly string[], value: string): string {
  const { parent, found, rest } = walk(text, path);
  if (found) {
    // Existing non-object value on an intermediate segment lands here via
    // walk's rest; a found full path is always a plain value replacement.
    return text.slice(0, found.valueStart) + q(value) + text.slice(found.valueEnd);
  }
  const objectText = text.slice(parent.open, parent.close + 1);
  const multiline = objectText.includes("\n");
  const last = parent.members.at(-1);
  if (last) {
    const indent = multiline ? lineIndent(text, last.keyStart) : "";
    const unit = multiline ? indentUnit(lineIndent(text, parent.open), indent) : "";
    const memberText = renderNested(rest, value, multiline, indent, unit);
    // Respect an existing trailing comma; otherwise add the separator.
    const afterLast = skipTrivia(text, last.valueEnd);
    const hasTrailingComma = text[afterLast] === ",";
    const insertAt = hasTrailingComma ? afterLast + 1 : last.valueEnd;
    const separator = hasTrailingComma ? "" : ",";
    // ponytail: new members use the house style (": " when multiline, ":" when
    // compact) rather than sniffing the file's colon spacing.
    const glue = multiline ? `${separator}\n${indent}` : separator;
    return text.slice(0, insertAt) + glue + memberText + text.slice(insertAt);
  }
  // Empty object: rewrite its span.
  const indent = multiline || text.includes("\n") ? lineIndent(text, parent.open) : "";
  const grow = multiline || text.includes("\n");
  const unit = grow ? "  " : "";
  const memberText = renderNested(rest, value, grow, indent + unit, unit);
  const replacement = grow ? `{\n${indent + unit}${memberText}\n${indent}}` : `{${memberText}}`;
  return text.slice(0, parent.open) + replacement + text.slice(parent.close + 1);
}

/** Whether a (string) value exists at path. */
export function hasJsoncKey(text: string, path: readonly string[]): boolean {
  return walk(text, path).found !== undefined;
}

/** Remove the member at path. Absent key (or section) is a no-op. */
export function removeJsoncKey(
  text: string,
  path: readonly string[],
): { text: string; removed: boolean } {
  const { parent, found } = walk(text, path);
  if (!found) return { text, removed: false };

  // The member's span: its whole line when it sits alone on one (including a
  // trailing comma and a trailing comment), so comments on OTHER members are
  // never touched.
  const lineStart = text.lastIndexOf("\n", found.keyStart - 1) + 1;
  const ownsLine = /^[ \t]*$/.test(text.slice(lineStart, found.keyStart));
  const start = ownsLine ? lineStart : found.keyStart;
  const afterValue = skipTrivia(text, found.valueEnd);
  const hasComma = text[afterValue] === ",";
  let end = hasComma ? afterValue + 1 : found.valueEnd;
  if (ownsLine) {
    // Swallow the rest of the line (whitespace or the member's own trailing
    // comment) and the newline.
    const nl = text.indexOf("\n", end);
    if (nl >= 0 && /^[ \t]*(\/\/.*)?$/.test(text.slice(end, nl))) end = nl + 1;
  }
  let out = text.slice(0, start) + text.slice(end);

  // A last member without its own trailing comma leaves the previous member's
  // separator dangling; drop that single comma character (comments stay).
  const index = parent.members.indexOf(found);
  const previous = parent.members[index - 1];
  if (!hasComma && index === parent.members.length - 1 && previous) {
    const afterPrevious = skipTrivia(text, previous.valueEnd);
    if (text[afterPrevious] === ",") {
      out = out.slice(0, afterPrevious) + out.slice(afterPrevious + 1);
    }
  }
  return { text: out, removed: true };
}
