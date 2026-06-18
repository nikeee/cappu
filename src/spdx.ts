// Validation for the cappu.json "license" field: an npm-style SPDX license
// expression (a single id like "MIT", or a compound like "(MIT OR Apache-2.0)"
// and "GPL-2.0-only WITH Classpath-exception-2.0"). Only SPDX is accepted, so a
// free-text license name ("The Apache Software License, Version 2.0") is
// rejected. Ids are checked against a curated set of the SPDX identifiers Java
// projects use in practice - the full SPDX list is ~700 entries; add more here
// as needed.

const LICENSE_IDS = new Set<string>([
  "0BSD",
  "AGPL-3.0-only",
  "AGPL-3.0-or-later",
  "Apache-1.1",
  "Apache-2.0",
  "Artistic-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "BSD-4-Clause",
  "BSL-1.0",
  "CC0-1.0",
  "CC-BY-4.0",
  "CC-BY-SA-4.0",
  "CDDL-1.0",
  "CDDL-1.1",
  "EPL-1.0",
  "EPL-2.0",
  "EUPL-1.2",
  "GPL-2.0-only",
  "GPL-2.0-or-later",
  "GPL-3.0-only",
  "GPL-3.0-or-later",
  "ISC",
  "LGPL-2.1-only",
  "LGPL-2.1-or-later",
  "LGPL-3.0-only",
  "LGPL-3.0-or-later",
  "MIT",
  "MIT-0",
  "MPL-1.1",
  "MPL-2.0",
  "Unlicense",
  "WTFPL",
  "Zlib",
  // deprecated but still commonly written short forms
  "AGPL-3.0",
  "GPL-2.0",
  "GPL-3.0",
  "LGPL-2.1",
  "LGPL-3.0",
]);

const EXCEPTION_IDS = new Set<string>([
  "Classpath-exception-2.0",
  "GPL-3.0-linking-exception",
  "LLVM-exception",
  "OpenJDK-assembly-exception-1.0",
]);

/**
 * Whether `expression` is a valid SPDX license expression: ids from the known
 * set, combined with AND / OR / parentheses, an optional `+` (or-later), and
 * `<id> WITH <exception>`.
 */
export function isValidSpdxExpression(expression: string): boolean {
  const tokens = expression
    .replaceAll("(", " ( ")
    .replaceAll(")", " ) ")
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  if (tokens.length === 0) return false;

  let expectOperand = true; // a license id or "(" comes next
  let depth = 0;
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]!;
    if (token === "(") {
      if (!expectOperand) return false;
      depth++;
    } else if (token === ")") {
      if (expectOperand || depth === 0) return false;
      depth--;
    } else if (token === "AND" || token === "OR") {
      if (expectOperand) return false;
      expectOperand = true;
    } else {
      if (!expectOperand) return false;
      const id = token.endsWith("+") ? token.slice(0, -1) : token;
      if (!LICENSE_IDS.has(id)) return false;
      if (tokens[i + 1] === "WITH") {
        if (!tokens[i + 2] || !EXCEPTION_IDS.has(tokens[i + 2]!)) return false;
        i += 2;
      }
      expectOperand = false;
    }
  }
  return !expectOperand && depth === 0;
}
