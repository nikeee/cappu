---
name: ci-release
description: "Use when wiring a cappu Java project into CI (GitHub, Forgejo or Gitea Actions), writing its Dockerfile, uploading cappu audit SARIF, choosing which cappu commands gate a pipeline, bumping the project version, or publishing a jar to Maven Central or a private Maven registry. Covers install --locked, verify, version, publish and the CI exit codes."
---

# CI, Docker and releases with cappu

There is no `setup-cappu` action and no npm package: CI downloads the static
binary from a GitHub release, Docker copies it out of the
`ghcr.io/nikeee/cappu:latest` image. Reproducibility comes from
`cappu-lock.json` plus `cappu install --locked`, which refuses to download when
the lock is stale. Complete, copyable files are in
`references/ci-and-docker-recipes.md`.

## The gate ladder

Run in this order; each exits non-zero on failure, so a plain shell step fails
the job:

| Step | Command | Fails when |
|---|---|---|
| 1 | `cappu install --locked` | `cappu-lock.json` missing or out of sync with `cappu.json` (checked before any download), or a downloaded jar does not match its hash |
| 2 | `cappu format` | any file is not google-java-format clean (flagless = check mode; never use `-w` in CI) |
| 3 | `cappu check` | cappu's checker reports an error (stricter than javac; drop this step if it flags code javac accepts) |
| 4 | `cappu compile -q` (or `-o fat-jar --artifact app`) | javac error |
| 5 | `cappu test` | any JUnit failure, or no tests under `src/test/java` |
| 6 | `cappu audit --format sarif > cappu-audit.sarif` | any known vulnerability (exit 1). Upload the SARIF, then fail the job |

Not a gate: `cappu outdated` exits 0 regardless of findings. `cappu verify`
re-checks installed jars against the lock and is useful after a cache restore.

Diagnostics print `::error file=...,line=...::msg` automatically when
`GITHUB_ACTIONS`, `FORGEJO_ACTIONS` or `GITEA_ACTIONS` is `true`.

## Procedure: GitHub Actions

1. **Install the binary.**
   `curl -fsSL -o /usr/local/bin/cappu https://github.com/nikeee/cappu/releases/download/v<version>/cappu-linux-x64 && chmod +x /usr/local/bin/cappu`.
   `releases/latest/download/...` also works but is not reproducible.
2. **Provide a JDK.** Either `"jdk": "temurin-21"` in `cappu.json` (cappu
   downloads it into `~/.cache/cappu`; no `setup-java` needed) or
   `actions/setup-java`. `cappu test` and `cappu run` need `java` from that JDK.
3. **Cache `~/.cache/cappu`** keyed on `hashFiles('cappu-lock.json', 'cappu.json')`.
   It holds the jars and the provisioned JDK.
4. **Run the ladder.** For audit put `continue-on-error: true` on the step,
   upload with `github/codeql-action/upload-sarif`, then add a final step that
   fails when the audit step's outcome was `failure`.
5. **Agent variables are absent in CI**, so pass `--json` or `--format sarif`
   explicitly wherever you parse output.

## Procedure: Docker

Multi-stage, with config and lock bind-mounted so the dependency layer is cached
independently of source changes:

```Dockerfile
FROM eclipse-temurin:21-jdk AS build
WORKDIR /code
COPY --from=ghcr.io/nikeee/cappu:latest /cappu /cappu
RUN --mount=type=bind,source=cappu.json,target=cappu.json \
    --mount=type=bind,source=cappu-lock.json,target=cappu-lock.json \
    --mount=type=cache,target=/root/.cache/cappu \
    /cappu install --locked
COPY ./ ./
RUN --mount=type=cache,target=/root/.cache/cappu \
    /cappu compile -q -o fat-jar --artifact app

FROM eclipse-temurin:21-jre
COPY --from=build /code/dist/app.jar /app.jar
ENTRYPOINT ["java", "-jar", "/app.jar"]
```

`.dockerignore` must list `.cappu` and `dist`. With `"jdk"` set in `cappu.json`
the build stage can be any base image; cappu provisions the JDK into the cache
mount. `--artifact app` avoids guessing the `<artifactId>-<version>.jar` name.

## Procedure: release

1. **Commit everything first.** `cappu version major|minor|patch` (only these
   three words; `cappu version 1.2.3` is exit 2) writes the new semver into
   `cappu.json`, prints `vX.Y.Z`, and when run at the git root commits
   `cappu.json` and creates the tag `vX.Y.Z`. A git failure is only a warning;
   the bump still happens.
2. **Push with tags.** `git push --follow-tags`.
3. **Publish from CI on the tag.** `cappu publish [--repo <url>]` builds the
   jar with javac, generates the POM from `groupId`, `artifactId`, `version` and
   the dependency maps, and uploads jar, POM and `.md5`/`.sha1` sidecars in
   maven2 layout.
   - Registry precedence: `--repo`, then `CAPPU_PUBLISH_REGISTRY`, then
     `publishRepository` in `cappu.json`, then Maven Central.
   - Credentials, env only: `CAPPU_PUBLISH_USERNAME` plus `CAPPU_PUBLISH_PASSWORD`
     (basic auth) or `CAPPU_PUBLISH_TOKEN` (bearer). Missing credentials or
     missing coordinates: exit 2 before anything is built. Compile or upload
     failure: exit 1.

**Gotcha - private registries are publish-only.** Resolving dependencies from a
registry that needs credentials is not supported (`packageSources` are anonymous
URLs). Publishing to one works.

**Gotcha - POM scopes follow Gradle's published-POM rules.** `api` becomes
Maven `compile`, `implementation` becomes `runtime`, `testImplementation`
becomes `test`, `annotationProcessor` is left out. Consumers cannot compile
against a `runtime` dependency, so any type that appears in your public API
must be declared under `api`. Former Maven `provided` or `optional` deps that
were folded into `implementation` are published as `runtime` and bundled into
a fat jar.

## Gotchas checklist

- [ ] Binary download pinned to a release tag, not `latest`, for reproducible CI.
- [ ] `~/.cache/cappu` (or the Docker cache mount) cached, keyed on the lock.
- [ ] `install --locked`, not plain `install`, in every pipeline.
- [ ] `format` flagless as the gate; `-w` only locally.
- [ ] `audit` SARIF uploaded before the job fails on it.
- [ ] `.dockerignore` has `.cappu` and `dist`.
- [ ] Fat jar copied by its real name (`--artifact` or `<artifactId>-<version>.jar`).
- [ ] `cappu version` runs `git commit` and `git tag`; working tree clean before.
- [ ] Publish secrets as `CAPPU_PUBLISH_*` env; nothing in `cappu.json`.
