// Package licenses. Maven's <license><name> is free text, not an SPDX
// identifier (the same license shows up as "Apache-2.0", "Apache License,
// Version 2.0", "The Apache Software License, Version 2.0", ...), so we keep
// the raw declaration and offer a best-effort SPDX normalization beside it.

import { type Brand } from "../brand.ts";

/** A canonical SPDX license id ("Apache-2.0"), as opposed to a raw POM name. */
export type SpdxId = Brand<string, "SpdxId">;

/** One license exactly as a POM's <licenses> declares it (raw, not SPDX). */
export interface License {
  readonly name: string;
  readonly url?: string;
}

// A loose key for matching: lowercased, every run of non-alphanumerics folded
// to a single space. Collapses spelling/spacing/punctuation variants so one
// table entry covers "Apache-2.0", "Apache 2.0", "Apache License, Version 2.0".
function normalizeKey(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, " ")
    .trim();
}

// normalizeKey(raw) -> SPDX id (or SPDX expression for dual licenses). Seeded by
// parsing the declared <license><name> strings of the most-depended-on Maven
// Central packages (gson, guava, jackson, junit, slf4j, log4j, okhttp, lombok,
// netty, postgresql, ...); add new variants here as they are encountered.
const SPDX_ALIASES: Record<string, string> = {
  "apache 2 0": "Apache-2.0",
  "apache license version 2 0": "Apache-2.0",
  "apache software license version 2 0": "Apache-2.0",
  "the apache license version 2 0": "Apache-2.0",
  "the apache software license version 2 0": "Apache-2.0",
  "bsd 2 clause": "BSD-2-Clause",
  "bsd 3 clause": "BSD-3-Clause",
  "bsd license 3": "BSD-3-Clause",
  "cddl gplv2 with classpath exception": "CDDL-1.1 OR GPL-2.0-with-classpath-exception",
  "gpl2 w cpe": "GPL-2.0-with-classpath-exception",
  "eclipse public license 1 0": "EPL-1.0",
  "eclipse public license v 1 0": "EPL-1.0",
  "epl 1 0": "EPL-1.0",
  "eclipse public license v2 0": "EPL-2.0",
  "eclipse public license version 2 0": "EPL-2.0",
  "epl 2 0": "EPL-2.0",
  "gnu lesser general public license": "LGPL-2.1",
  mit: "MIT",
  "mit license": "MIT",
  "the mit license": "MIT",
  "mpl 2 0": "MPL-2.0",
};

// Canonical license-deed URLs (substrings of the normalized <license><url>)
// mapped to their SPDX id - the fallback when the name does not match. Many
// POMs point a vague or vendor-specific <name> at a precise, well-known url, so
// the url disambiguates. Matched as a substring against the url lowercased with
// the scheme and any "www." / trailing slash stripped.
const SPDX_URL_PATTERNS: [needle: string, spdx: string][] = [
  ["oss.oracle.com/licenses/cddl+gpl", "CDDL-1.1 OR GPL-2.0-with-classpath-exception"],
  ["classpath/license", "GPL-2.0-with-classpath-exception"],
  ["apache.org/licenses/license-2.0", "Apache-2.0"],
  ["opensource.org/licenses/apache-2.0", "Apache-2.0"],
  ["opensource.org/licenses/bsd-3-clause", "BSD-3-Clause"],
  ["opensource.org/licenses/bsd-2-clause", "BSD-2-Clause"],
  ["eclipse.org/legal/epl-2.0", "EPL-2.0"],
  ["eclipse.org/legal/epl-v20", "EPL-2.0"],
  ["eclipse.org/legal/epl-v10", "EPL-1.0"],
  ["eclipse.org/legal/epl-1.0", "EPL-1.0"],
  ["opensource.org/licenses/eclipse-1.0", "EPL-1.0"],
  ["mozilla.org/en-us/mpl/2.0", "MPL-2.0"],
  ["mozilla.org/mpl/2.0", "MPL-2.0"],
  ["gnu.org/licenses/old-licenses/lgpl-2.1", "LGPL-2.1"],
  ["opensource.org/licenses/mit", "MIT"],
];

function spdxFromUrl(url: string): string | undefined {
  const normalized = url
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .replace(/^www\./, "")
    .replace(/\/+$/, "");
  for (const [needle, spdx] of SPDX_URL_PATTERNS) {
    if (normalized.includes(needle)) return spdx;
  }
  return undefined;
}

/**
 * Best-effort SPDX id for a license: the name first, then (when the name does
 * not match) a known license url. Undefined when neither is recognized.
 */
export function normalizeLicense(name: string, url?: string): SpdxId | undefined {
  return (SPDX_ALIASES[normalizeKey(name)] ?? (url ? spdxFromUrl(url) : undefined)) as
    | SpdxId
    | undefined;
}

/** The deduped SPDX ids `licenses` map to (drops the ones with no mapping). */
export function normalizeLicenses(licenses: readonly License[]): SpdxId[] {
  const ids = new Set<SpdxId>();
  for (const license of licenses) {
    const spdx = normalizeLicense(license.name, license.url);
    if (spdx) ids.add(spdx);
  }
  return [...ids];
}
