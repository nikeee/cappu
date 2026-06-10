import { test } from "node:test";

import { expect } from "expect";

import { MavenRepositorySource, parseMetadataVersions, parsePom } from "./maven.ts";

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

const COORDS = { groupId: "org.example", artifactId: "app", version: "1.0" };

test("maven-metadata.xml versions parse in document order", () => {
  expect(parseMetadataVersions(METADATA)).toEqual(["3.12.0", "3.13.0", "3.14.0"]);
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

  expect(await source.getMetadata({ ...COORDS, version: "0.404" })).toBeUndefined();
  expect(await source.search()).toEqual([]);
});
