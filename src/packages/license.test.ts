import { test } from "node:test";

import { expect } from "expect";

import { normalizeLicense, normalizeLicenses } from "./license.ts";

test("spelling, spacing and punctuation variants map to the same SPDX id", () => {
  for (const name of [
    "Apache-2.0",
    "Apache License, Version 2.0",
    "The Apache Software License, Version 2.0",
    "Apache Software License - Version 2.0",
  ]) {
    expect(normalizeLicense(name)).toBe("Apache-2.0");
  }
  expect(normalizeLicense("MIT")).toBe("MIT");
  expect(normalizeLicense("The MIT License")).toBe("MIT");
  expect(normalizeLicense("Eclipse Public License - v 1.0")).toBe("EPL-1.0");
  expect(normalizeLicense("EPL 2.0")).toBe("EPL-2.0");
  expect(normalizeLicense("New BSD License")).toBe("BSD-3-Clause");
});

// The exact <license><name> strings declared by common Maven Central packages
// (gson, guava, jackson, junit, slf4j, log4j, okhttp, lombok, netty,
// postgresql, ...) - the data the default mapping was built from.
test("the declared license names of common packages all normalize", () => {
  const expected: Record<string, string> = {
    "Apache License, Version 2.0": "Apache-2.0",
    "Apache Software License - Version 2.0": "Apache-2.0",
    "Apache-2.0": "Apache-2.0",
    "The Apache License, Version 2.0": "Apache-2.0",
    "The Apache Software License, Version 2.0": "Apache-2.0",
    "BSD License 3": "BSD-3-Clause",
    "BSD-2-Clause": "BSD-2-Clause",
    "BSD-3-Clause": "BSD-3-Clause",
    "CDDL + GPLv2 with classpath exception": "CDDL-1.1 OR GPL-2.0-with-classpath-exception",
    "GPL2 w/ CPE": "GPL-2.0-with-classpath-exception",
    "Eclipse Public License - v 1.0": "EPL-1.0",
    "Eclipse Public License 1.0": "EPL-1.0",
    "EPL 1.0": "EPL-1.0",
    "Eclipse Public License - Version 2.0": "EPL-2.0",
    "Eclipse Public License v2.0": "EPL-2.0",
    "EPL 2.0": "EPL-2.0",
    "GNU Lesser General Public License": "LGPL-2.1",
    MIT: "MIT",
    "MIT License": "MIT",
    "The MIT License": "MIT",
    "MPL 2.0": "MPL-2.0",
  };
  for (const [name, spdx] of Object.entries(expected)) {
    expect(normalizeLicense(name)).toBe(spdx);
  }
});

// The exact <license><name> strings that installing jmh and the jakarta xml
// bind stack surfaced as "no SPDX mapping" during the Maven->cappu migration.
test("licenses seen during real migrations normalize", () => {
  expect(
    normalizeLicense("GNU General Public License (GPL), version 2, with the Classpath exception"),
  ).toBe("GPL-2.0-with-classpath-exception");
  expect(normalizeLicense("Eclipse Distribution License - v 1.0")).toBe("BSD-3-Clause");
  expect(normalizeLicense("EDL 1.0")).toBe("BSD-3-Clause");
  // EDL by url (name absent/vague)
  expect(normalizeLicense("EDL", "http://www.eclipse.org/org/documents/edl-v10.php")).toBe(
    "BSD-3-Clause",
  );
});

test("a known license url normalizes when the name does not", () => {
  // vague/vendor name, but a canonical deed url - the url disambiguates
  expect(
    normalizeLicense("Custom Vendor Terms", "https://www.apache.org/licenses/LICENSE-2.0.txt"),
  ).toBe("Apache-2.0");
  expect(normalizeLicense("LGPL", "http://www.gnu.org/licenses/old-licenses/lgpl-2.1.html")).toBe(
    "LGPL-2.1",
  );
  expect(normalizeLicense("EPL", "https://www.eclipse.org/legal/epl-2.0/")).toBe("EPL-2.0");
  // the name still wins when it maps, whatever the url
  expect(normalizeLicense("MIT License", "https://example.com/whatever")).toBe("MIT");
});

test("an unrecognized license name and url have no mapping", () => {
  expect(normalizeLicense("Public Domain")).toBeUndefined();
  expect(normalizeLicense("Weird Custom License 1.3")).toBeUndefined();
  expect(normalizeLicense("Custom", "https://example.com/license.txt")).toBeUndefined();
});

test("normalizeLicenses dedupes and drops the unmapped names", () => {
  expect(
    normalizeLicenses([
      { name: "Apache-2.0" },
      { name: "The Apache Software License, Version 2.0" }, // same id, deduped
      { name: "MIT License" },
      { name: "Public Domain" }, // unmapped, dropped
    ]),
  ).toEqual(["Apache-2.0", "MIT"]);
});
