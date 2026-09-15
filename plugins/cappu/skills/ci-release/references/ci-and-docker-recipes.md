# CI and Docker recipes for cappu

Copy, then adjust the cappu version, JDK and registry. Every gate is a plain
`run:` step because each cappu command exits non-zero on failure.

## GitHub Actions: build, test, audit, publish on tag

```yaml
name: CI
on:
  push:
    branches: [main]
    tags: ["v*"]
  pull_request:

permissions:
  contents: read
  security-events: write # SARIF upload

env:
  CAPPU_VERSION: "0.1.23" # pin; releases/latest/download/... also works but is not reproducible

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install cappu
        run: |
          curl -fsSL -o /usr/local/bin/cappu \
            "https://github.com/nikeee/cappu/releases/download/v${CAPPU_VERSION}/cappu-linux-x64"
          chmod +x /usr/local/bin/cappu
          cappu --version

      # Drop this step when cappu.json pins a "jdk"; cappu provisions it into ~/.cache/cappu.
      - uses: actions/setup-java@v4
        with:
          distribution: temurin
          java-version: "21"

      - uses: actions/cache@v4
        with:
          path: ~/.cache/cappu
          key: cappu-${{ runner.os }}-${{ hashFiles('cappu-lock.json', 'cappu.json') }}

      - run: cappu install --locked
      - run: cappu format
      - run: cappu check
      - run: cappu compile -q
      - run: cappu test

      # Only useful with "testOptions": { "outputFormat": "junit" } in cappu.json.
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: test-results
          path: dist/test-results
          if-no-files-found: ignore

      - name: Audit dependencies
        id: audit
        continue-on-error: true
        run: cappu audit --format sarif > cappu-audit.sarif

      - uses: github/codeql-action/upload-sarif@v3
        if: steps.audit.outcome != 'skipped'
        with:
          sarif_file: cappu-audit.sarif

      - name: Fail on vulnerabilities
        if: steps.audit.outcome == 'failure'
        run: exit 1

  publish:
    needs: build
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install cappu
        run: |
          curl -fsSL -o /usr/local/bin/cappu \
            "https://github.com/nikeee/cappu/releases/download/v${CAPPU_VERSION}/cappu-linux-x64"
          chmod +x /usr/local/bin/cappu
      - uses: actions/setup-java@v4
        with:
          distribution: temurin
          java-version: "21"
      - run: cappu install --locked
      - run: cappu publish
        env:
          # Or set "publishRepository" in cappu.json and drop this line.
          CAPPU_PUBLISH_REGISTRY: ${{ vars.MAVEN_REGISTRY }}
          CAPPU_PUBLISH_USERNAME: ${{ secrets.MAVEN_USERNAME }}
          CAPPU_PUBLISH_PASSWORD: ${{ secrets.MAVEN_PASSWORD }}
          # Bearer alternative: CAPPU_PUBLISH_TOKEN: ${{ secrets.MAVEN_TOKEN }}
```

Forgejo and Gitea Actions run the same YAML minus the `upload-sarif` step (keep
the SARIF as an artifact instead). Annotations (`::error file=...`) appear
automatically because those runners set `FORGEJO_ACTIONS` or `GITEA_ACTIONS`.

The release itself is cut locally: `cappu version patch` (or `minor`, `major`)
bumps `cappu.json`, commits it and tags `vX.Y.Z` when run at the git root; then
`git push --follow-tags` triggers the `publish` job.

## Dockerfile: multi-stage fat jar

```Dockerfile
# syntax=docker/dockerfile:1
FROM eclipse-temurin:21-jdk AS build
WORKDIR /code
COPY --from=ghcr.io/nikeee/cappu:latest /cappu /cappu

# Dependency layer: only cappu.json and the lock are visible, so this layer is
# reused until a dependency changes. The cache mount keeps downloaded jars (and
# a provisioned JDK, if cappu.json pins one) across builds.
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

`.dockerignore`:

```
.cappu
dist
.git
```

Notes:

- `--artifact app` makes the jar name independent of `artifactId` and
  `version`. Without it the jar is `<artifactId>-<version>.jar`, or
  `<directory name>.jar` when either coordinate is missing.
- `ghcr.io/nikeee/cappu:latest` is a `FROM scratch` image whose only content
  is the static binary at `/cappu`. There are no version tags on the image; pin
  by digest if you need reproducibility.
- If `cappu.json` pins `"jdk": "temurin-21"`, the build stage does not need a
  JDK base image; cappu downloads the JDK into the cache mount. The runtime
  stage still needs a JRE.
- Spring Boot works from the fat jar without a plugin: cappu merges
  `META-INF/spring.factories`, `AutoConfiguration.imports` and
  `META-INF/services/*` from all dependency jars.
