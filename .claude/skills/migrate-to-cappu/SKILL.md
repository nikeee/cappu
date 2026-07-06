---
name: migrate-to-cappu
description: "Use when migrating a Maven or Gradle Java project to cappu - translating pom.xml/build.gradle into cappu.json, removing the old build files, and getting it to build/test. Covers single-module and multi-module projects, annotation processors, and the known gotchas."
---

# Migrate a Maven/Gradle project to cappu

There is no `cappu migrate` command. Migration is a manual translation of the
build file into `cappu.json`. This skill is that procedure, distilled from
migrating commons-lang, mapstruct (core + processor), and microexpressions.
See `docs/migration-findings.md` for the feature gaps behind these steps.

## Procedure

1. **Read the build file(s).** For Maven: the module `pom.xml` **and** its
   `<parent>` chain (versions and the java release usually live in the parent,
   not the module). For a multi-module build, do this per module.
2. **Write `cappu.json`** at each module root (mapping table below).
3. **Remove the old build files:**
   ```
   rm -rf pom.xml build.gradle settings.gradle .mvn mvnw mvnw.cmd \
          gradlew gradlew.bat gradle
   ```
4. **Build/verify loop:**
   ```
   cappu install     # resolve + download deps into .cappu/lib
   cappu compile     # javac main sources -> classes/jar/fat-jar
   cappu test        # compile src/test/java + run JUnit (JUnit 5 / Vintage)
   ```
   Iterate on errors. `cappu compile -q` hides the per-class output.

## pom.xml -> cappu.json mapping

| Maven | cappu.json |
|-------|------------|
| `<groupId>` / `<artifactId>` / `<version>` | top-level `groupId` / `artifactId` / `version` (semver; `-SNAPSHOT` is a valid prerelease) |
| `maven.compiler.release` / `source` / `target` / `java.version` | `compilerOptions.release` (integer) |
| dependency scope `compile` | `dependencies.implementation` (use `api` only if the type leaks into your public API) |
| dependency scope `test` | `dependencies.testImplementation` |
| dependency scope `provided` / `optional` / `runtime` | `dependencies.implementation` (**no exact equivalent** - lossy; fine for building, wrong for publishing) |
| annotation processor (compiler-plugin `annotationProcessorPaths`, or a `*-processor` artifact) | `dependencies.annotationProcessor` |
| non-default `<sourceDirectory>` / build-helper generated sources | `compilerOptions.sourcePaths` (array) |
| a runnable main | `compilerOptions.mainClass` |
| fat/shaded jar | `compilerOptions.output: "fat-jar"` (else `"classes"` or `"jar"`) |

Dependency coordinates are `"group:artifact": "version"`. **Get the groupId
right** - e.g. mapstruct's gem tools are `org.mapstruct.tools.gem:gem-api`, not
`org.mapstruct:gem-api`. A wrong coordinate fails install with
`not found in any package source`.

Minimal example (single module):
```jsonc
{
  "groupId": "de.micromata",
  "artifactId": "microexpressions",
  "version": "0.1.1",
  "compilerOptions": { "release": 17 },
  "dependencies": {
    "implementation": {
      "org.apache.commons:commons-collections4": "4.5.0",
      "net.bytebuddy:byte-buddy": "1.18.10"
    },
    "testImplementation": { "org.junit.jupiter:junit-jupiter": "6.1.1" }
  }
}
```

## Resolving versions (the tedious part)

cappu.json needs **every version pinned literally**. It does not read parent
POMs, `<dependencyManagement>`, BOM imports, or `${property}` placeholders. When
a `<dependency>` has no `<version>` (or uses `${...}`):
- Resolve the property in the module pom, then its parent pom(s).
- For BOM-managed versions (junit-bom, kotlin-bom, ...), fetch the parent pom
  and grep the `<properties>` block, e.g.
  `curl -s https://repo.maven.apache.org/maven2/org/apache/commons/commons-parent/102/commons-parent-102.pom`.
- `cappu show group:artifact` prints the latest published version if you just
  want current.

## Multi-module projects

cappu has no reactor/workspace. Treat **each module as its own cappu project**
(one `cappu.json` per module) and wire them by hand:

1. Determine build order from inter-module deps (e.g. mapstruct
   `core -> processor`).
2. Build upstream modules first (`output: "jar"`), then reference the produced
   jar from the downstream module's `compilerOptions.classPath`.

**Gotcha - `classPath` replaces the defaults, it does not append.** The default
classpath includes `./.cappu/lib/classes` (where installed deps land). If you
set `classPath` to add a sibling jar, you must **re-list the default**, or every
resolved dependency drops off the compile path:
```jsonc
"compilerOptions": {
  "classPath": [
    "./.cappu/lib/classes",                          // keep this!
    "../core/dist/mapstruct-1.7.0-SNAPSHOT.jar"      // the sibling module's jar
  ]
}
```

**Skip aggregator/relocation modules.** A module with
`<packaging>pom</packaging>`, only a pom and no sources, or a
`<distributionManagement><relocation>` (e.g. mapstruct's `mapstruct-jdk8`) has
nothing to build - do not create a cappu project for it.

## Gotchas checklist

- [ ] Versions all pinned literally (no `${...}`, no BOM inheritance).
- [ ] Right groupId per coordinate (install fails loudly on a wrong one).
- [ ] Custom `classPath`? Re-list `./.cappu/lib/classes`.
- [ ] Multi-module: build order set, upstream jars referenced.
- [ ] Aggregator/relocation modules skipped.
- [ ] After `cappu test`, missing-package errors may mean an **undeclared
      transitive test dep** (commons-lang needed `org.ow2.asm:asm` even though
      its pom doesn't list it) - add it explicitly.
- [ ] `provided`/`optional`/`runtime` became `implementation` - note it if you
      will publish.
- [ ] Install shows `version X loses to Y` warnings? A version conflict was
      resolved against your declared version; if tests misbehave at runtime,
      pin the conflicting transitive dep explicitly.
