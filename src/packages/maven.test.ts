import { test } from "node:test";

import { expect } from "expect";

import {
  fetchWithRetry,
  MavenRepositorySource,
  parseMetadataVersions,
  parsePom,
  toSolrQuery,
} from "./maven.ts";
import { toCoordinates } from "./types.ts";

const METADATA = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>org.apache.commons</groupId>
  <artifactId>commons-lang3</artifactId>
  <versioning>
    <latest>3.14.0</latest>
    <release>3.14.0</release>
    <versions>
      <version>3.12.0</version>
      <version>3.13.0</version>
      <version>3.14.0</version>
    </versions>
  </versioning>
</metadata>`;

const POM = `<?xml version="1.0"?>
<project>
  <groupId>org.example</groupId>
  <artifactId>app</artifactId>
  <version>1.0</version>
  <description>Example app</description>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>org.managed</groupId><artifactId>managed</artifactId><version>9</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>3.14.0</version>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.0</version>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>org.opt</groupId>
      <artifactId>opt</artifactId>
      <version>1</version>
      <optional>true</optional>
    </dependency>
    <dependency>
      <groupId>org.prop</groupId>
      <artifactId>prop</artifactId>
      <version>\${prop.version}</version>
    </dependency>
  </dependencies>
</project>`;

const COORDS = toCoordinates("org.example", "app", "1.0");

test("maven-metadata.xml versions parse in document order", () => {
  expect(parseMetadataVersions(METADATA)).toEqual(["3.12.0", "3.13.0", "3.14.0"]);
  // Malformed metadata yields no versions rather than throwing.
  expect(parseMetadataVersions("not xml at all")).toEqual([]);
});

test("pom dependencies parse with scope/optional; managed and property versions are skipped", () => {
  const metadata = parsePom(POM, COORDS);
  expect(metadata.description).toBe("Example app");
  expect(metadata.dependencies).toEqual([
    {
      groupId: "org.apache.commons",
      artifactId: "commons-lang3",
      version: "3.14.0",
      scope: undefined,
      optional: false,
    },
    {
      groupId: "org.junit.jupiter",
      artifactId: "junit-jupiter",
      version: "5.10.0",
      scope: "test",
      optional: false,
    },
    { groupId: "org.opt", artifactId: "opt", version: "1", scope: undefined, optional: true },
  ]);
  expect(metadata.incomplete).toBe(true); // the \${prop.version} dependency was dropped
});

test("licenses parse with raw names and best-effort SPDX normalization", () => {
  const pom = `<project>
    <groupId>org.example</groupId><artifactId>app</artifactId><version>1.0</version>
    <licenses>
      <license>
        <name>The Apache Software License, Version 2.0</name>
        <url>https://www.apache.org/licenses/LICENSE-2.0.txt</url>
      </license>
      <license><name>Public Domain</name></license>
    </licenses>
  </project>`;
  const metadata = parsePom(pom, COORDS);
  expect(metadata.licenses).toEqual([
    {
      name: "The Apache Software License, Version 2.0",
      url: "https://www.apache.org/licenses/LICENSE-2.0.txt",
    },
    { name: "Public Domain" },
  ]);
  // Public Domain has no SPDX id, so only the Apache license normalizes
  expect(metadata.licenseNormalized).toEqual(["Apache-2.0"]);
});

test("licenses are inherited from the nearest parent that declares any", async () => {
  const poms = new Map([
    [
      "/org/example/app/1.0/app-1.0.pom",
      `<project>
        <parent><groupId>org.example</groupId><artifactId>parent</artifactId><version>7</version></parent>
      </project>`,
    ],
    [
      "/org/example/parent/7/parent-7.pom",
      `<project>
        <licenses><license><name>MIT License</name></license></licenses>
      </project>`,
    ],
  ]);
  const source = new MavenRepositorySource("https://repo.example/maven2", async url =>
    poms.get(url.replace("https://repo.example/maven2", "")),
  );
  const metadata = await source.getMetadata(COORDS);
  expect(metadata?.licenses).toEqual([{ name: "MIT License" }]);
  expect(metadata?.licenseNormalized).toEqual(["MIT"]);
});

test("the repository source builds maven2 layout urls and parses the answers", async () => {
  const fetched: string[] = [];
  const source = new MavenRepositorySource("https://repo.example/maven2/", async url => {
    fetched.push(url);
    if (url.endsWith("/maven-metadata.xml")) return METADATA;
    if (url.endsWith("/app-1.0.pom")) return POM;
    return undefined;
  });

  expect(await source.listVersions("org.apache.commons", "commons-lang3")).toEqual([
    "3.12.0",
    "3.13.0",
    "3.14.0",
  ]);
  expect(fetched[0]).toBe(
    "https://repo.example/maven2/org/apache/commons/commons-lang3/maven-metadata.xml",
  );

  const metadata = await source.getMetadata(COORDS);
  expect(fetched[1]).toBe("https://repo.example/maven2/org/example/app/1.0/app-1.0.pom");
  expect(metadata?.dependencies.length).toBe(3);

  expect(
    await source.getMetadata(toCoordinates(COORDS.groupId, COORDS.artifactId, "0.404")),
  ).toBeUndefined();
  // no searchUrl configured: this repository is not searchable
  expect(await source.search("gson")).toEqual([]);
});

test("search queries the index service and tolerates broken answers", async () => {
  const fetched: string[] = [];
  let answer = JSON.stringify({
    response: {
      docs: [
        {
          g: "com.google.code.gson",
          a: "gson",
          latestVersion: "2.13.1",
          p: "jar",
          versionCount: 42,
          timestamp: 1671000000000,
        },
        { g: "org.partial", a: "no-version" }, // dropped: no latestVersion
      ],
    },
  });
  const source = new MavenRepositorySource(
    "https://repo.example/maven2",
    async url => {
      fetched.push(url);
      return answer;
    },
    undefined,
    "https://search.example/solrsearch/select",
  );

  expect(await source.search("gso n")).toEqual([
    {
      groupId: "com.google.code.gson",
      artifactId: "gson",
      version: "2.13.1",
      packaging: "jar",
      versionCount: 42,
      lastUpdated: 1671000000000,
    },
  ]);
  expect(fetched[0]).toBe("https://search.example/solrsearch/select?q=gso+n&rows=20&wt=json");

  answer = "not json at all";
  expect(await source.search("gson")).toEqual([]);
});

test("a group:artifact query is translated to a structured solr query", async () => {
  expect(toSolrQuery("org.apache.commons:commons-lang3")).toBe(
    'g:"org.apache.commons" AND a:"commons-lang3"',
  );
  expect(toSolrQuery("commons-lang3")).toBe("commons-lang3");
  expect(toSolrQuery("foo bar")).toBe("foo bar");

  const fetched: string[] = [];
  const source = new MavenRepositorySource(
    "https://repo.example/maven2",
    async url => {
      fetched.push(url);
      return JSON.stringify({ response: { docs: [] } });
    },
    undefined,
    "https://search.example/solrsearch/select",
  );
  await source.search("org.apache.commons:commons-lang3");
  expect(fetched[0]).toBe(
    "https://search.example/solrsearch/select?q=g%3A%22org.apache.commons%22+AND+a%3A%22commons-lang3%22&rows=20&wt=json",
  );
});

test("locally defined properties interpolate into dependency versions", () => {
  const pom = `<project>
    <properties><lib.version>2.5</lib.version></properties>
    <dependencies>
      <dependency><groupId>g</groupId><artifactId>lib</artifactId><version>\${lib.version}</version></dependency>
      <dependency><groupId>\${project.groupId}</groupId><artifactId>sibling</artifactId><version>\${project.version}</version></dependency>
    </dependencies>
  </project>`;
  const metadata = parsePom(pom, COORDS);
  expect(metadata.incomplete).toBe(false);
  expect(metadata.dependencies).toEqual([
    { groupId: "g", artifactId: "lib", version: "2.5", scope: undefined, optional: false },
    {
      groupId: "org.example",
      artifactId: "sibling",
      version: "1.0",
      scope: undefined,
      optional: false,
    },
  ]);
});

// the multi-module pattern Central actually serves (jackson, httpclient5, ...):
// versions live in parent properties and grandparent dependencyManagement
test("getMetadata resolves versions through the parent chain", async () => {
  const poms = new Map([
    [
      "/org/example/app/1.0/app-1.0.pom",
      `<project>
        <parent><groupId>org.example</groupId><artifactId>parent</artifactId><version>7</version></parent>
        <properties><lib.version>3.1</lib.version></properties>
        <dependencies>
          <dependency><groupId>g</groupId><artifactId>from-prop</artifactId><version>\${lib.version}</version></dependency>
          <dependency><groupId>g</groupId><artifactId>from-mgmt</artifactId></dependency>
          <dependency><groupId>g</groupId><artifactId>unmanaged</artifactId></dependency>
        </dependencies>
      </project>`,
    ],
    [
      "/org/example/parent/7/parent-7.pom",
      `<project>
        <parent><groupId>org.example</groupId><artifactId>grandparent</artifactId><version>1</version></parent>
        <properties><lib.version>9.9</lib.version><mgmt.version>4.2</mgmt.version></properties>
      </project>`,
    ],
    [
      "/org/example/grandparent/1/grandparent-1.pom",
      `<project>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>from-mgmt</artifactId><version>\${mgmt.version}</version></dependency>
          <dependency><groupId>bom</groupId><artifactId>imported</artifactId><version>1</version><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
      </project>`,
    ],
  ]);
  const fetched: string[] = [];
  const source = new MavenRepositorySource("https://repo.example/maven2", async url => {
    fetched.push(url);
    return poms.get(url.replace("https://repo.example/maven2", ""));
  });

  const metadata = await source.getMetadata(COORDS);
  expect(metadata?.dependencies).toEqual([
    // the child's own property wins over the parent's value for lib.version
    { groupId: "g", artifactId: "from-prop", version: "3.1", scope: undefined, optional: false },
    // managed in the grandparent, interpolated with the parent's property
    { groupId: "g", artifactId: "from-mgmt", version: "4.2", scope: undefined, optional: false },
  ]);
  // `unmanaged` has no version anywhere; scope=import entries are not followed
  expect(metadata?.incomplete).toBe(true);

  // parent poms are cached: a second resolve fetches nothing new
  const before = fetched.length;
  await source.getMetadata(COORDS);
  expect(fetched.length).toBe(before);
});

test("scope=import BOMs are followed for managed versions", async () => {
  const poms = new Map([
    [
      "/org/example/app/1.0/app-1.0.pom",
      `<project>
        <properties><bom.version>3</bom.version></properties>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>both</artifactId><version>0.1</version></dependency>
          <dependency><groupId>org.example</groupId><artifactId>bom</artifactId><version>\${bom.version}</version><type>pom</type><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
        <dependencies>
          <dependency><groupId>g</groupId><artifactId>from-bom</artifactId></dependency>
          <dependency><groupId>g</groupId><artifactId>both</artifactId></dependency>
        </dependencies>
      </project>`,
    ],
    [
      "/org/example/bom/3/bom-3.pom",
      `<project>
        <properties><lib.version>7.5</lib.version></properties>
        <dependencyManagement><dependencies>
          <dependency><groupId>g</groupId><artifactId>from-bom</artifactId><version>\${lib.version}</version></dependency>
          <dependency><groupId>g</groupId><artifactId>both</artifactId><version>9.9</version></dependency>
          <dependency><groupId>org.example</groupId><artifactId>bom</artifactId><version>3</version><scope>import</scope></dependency>
        </dependencies></dependencyManagement>
      </project>`,
    ],
  ]);
  const source = new MavenRepositorySource("https://repo.example/maven2", async url =>
    poms.get(url.replace("https://repo.example/maven2", "")),
  );

  const metadata = await source.getMetadata(COORDS);
  expect(metadata?.incomplete).toBe(false);
  expect(metadata?.dependencies).toEqual([
    // managed in the imported BOM, interpolated with the BOM's own properties
    { groupId: "g", artifactId: "from-bom", version: "7.5", scope: undefined, optional: false },
    // the importing chain's own dependencyManagement wins over the BOM
    { groupId: "g", artifactId: "both", version: "0.1", scope: undefined, optional: false },
  ]);
  // the BOM importing itself (a cycle) terminated via the seen-set
});

test("a cyclic or missing parent chain terminates and reports incomplete", async () => {
  const poms = new Map([
    [
      "/g/a/1/a-1.pom",
      `<project>
        <parent><groupId>g</groupId><artifactId>b</artifactId><version>1</version></parent>
        <dependencies><dependency><groupId>g</groupId><artifactId>dep</artifactId></dependency></dependencies>
      </project>`,
    ],
    [
      "/g/b/1/b-1.pom",
      `<project>
        <parent><groupId>g</groupId><artifactId>a</artifactId><version>1</version></parent>
      </project>`,
    ],
    [
      "/g/orphan/1/orphan-1.pom",
      `<project>
        <parent><groupId>g</groupId><artifactId>gone</artifactId><version>1</version></parent>
        <dependencies><dependency><groupId>g</groupId><artifactId>dep</artifactId></dependency></dependencies>
      </project>`,
    ],
  ]);
  const source = new MavenRepositorySource("https://repo.example/maven2", async url =>
    poms.get(url.replace("https://repo.example/maven2", "")),
  );

  const cyclic = await source.getMetadata(toCoordinates("g", "a", "1"));
  expect(cyclic?.dependencies).toEqual([]);
  expect(cyclic?.incomplete).toBe(true);

  const orphan = await source.getMetadata(toCoordinates("g", "orphan", "1"));
  expect(orphan?.dependencies).toEqual([]);
  expect(orphan?.incomplete).toBe(true);
});

// Regression for nikeee/cappu#22: a 429 (Central rate-limiting a burst of POM
// fetches) must be retried, not silently treated as a 404-style miss that the
// resolver then reports as "not found in any package source".
//
// stubFetch replaces global fetch with a queue of canned responses (a status,
// optional body, optional retry-after), restoring it afterwards.
function stubFetch(responses: { status: number; body?: string; retryAfter?: string }[]): {
  calls: number;
  restore: () => void;
} {
  const original = globalThis.fetch;
  const state = { calls: 0, restore: () => (globalThis.fetch = original) };
  globalThis.fetch = (() => {
    const r = responses[Math.min(state.calls, responses.length - 1)]!;
    state.calls++;
    const headers = r.retryAfter ? { "retry-after": r.retryAfter } : undefined;
    return Promise.resolve(new Response(r.body ?? "", { status: r.status, headers }));
  }) as typeof fetch;
  return state;
}

const noSleep = (): Promise<void> => Promise.resolve();

test("fetchWithRetry retries a transient 429 then succeeds", async () => {
  const slept: number[] = [];
  const stub = stubFetch([{ status: 429 }, { status: 200, body: "ok" }]);
  try {
    const response = await fetchWithRetry("https://repo.example/maven2/a.pom", async ms => {
      slept.push(ms);
    });
    expect(await response?.text()).toBe("ok");
    expect(stub.calls).toBe(2);
    expect(slept).toEqual([500]); // one base-backoff sleep
  } finally {
    stub.restore();
  }
});

test("fetchWithRetry throws (not a miss) when the transient status persists", async () => {
  const stub = stubFetch([{ status: 503 }]);
  try {
    await expect(fetchWithRetry("https://repo.example/maven2/a.pom", noSleep)).rejects.toThrow(
      /HTTP 503 after 4 attempts/,
    );
    expect(stub.calls).toBe(4);
  } finally {
    stub.restore();
  }
});

test("fetchWithRetry treats a genuine 404 as a miss (no retry)", async () => {
  const stub = stubFetch([{ status: 404 }]);
  try {
    expect(await fetchWithRetry("https://repo.example/maven2/a.pom", noSleep)).toBeUndefined();
    expect(stub.calls).toBe(1);
  } finally {
    stub.restore();
  }
});

test("fetchWithRetry passes an AbortSignal so a stalled response cannot hang forever", async () => {
  const original = globalThis.fetch;
  let signal: AbortSignal | undefined;
  globalThis.fetch = ((_url: string, init?: RequestInit) => {
    signal = init?.signal ?? undefined;
    return Promise.resolve(new Response("ok", { status: 200 }));
  }) as typeof fetch;
  try {
    await fetchWithRetry("https://repo.example/maven2/a.pom", noSleep);
    expect(signal).toBeInstanceOf(AbortSignal);
  } finally {
    globalThis.fetch = original;
  }
});

test("fetchWithRetry honors a numeric Retry-After", async () => {
  const slept: number[] = [];
  const stub = stubFetch([
    { status: 429, retryAfter: "2" },
    { status: 200, body: "ok" },
  ]);
  try {
    await fetchWithRetry("https://repo.example/maven2/a.pom", async ms => {
      slept.push(ms);
    });
    expect(slept).toEqual([2000]); // 2s from Retry-After, not the 500ms default
  } finally {
    stub.restore();
  }
});
