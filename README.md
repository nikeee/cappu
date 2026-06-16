# cappu
A Java lsp server and toolchain. Favors convention over configuration.

```diff
- warning -
This is an entirely vibe-coded project that acts as an experiment.
I wanted to know how far I can get by steering an LLM under certain conditions.

Plus I was annoyed by the Java LSP plugin for VSC. :(
```
You can see all prompts [here](https://github.com/nikeee/cappu/blob/main/PROMPTS.md). You probably shouldn't use this project.

## Why?
I was annoyed by the Java extension for VSC that everyone uses. So I vibed my own LSP server. And then added a compiler[^1] and package manager because why not? Now I've got an LSP server, a package management solution and build system.

[^1]: Entirely opt-in, disabled by default. We're using standard `javac`.

I loathe ./gradlew and ./mvnw with random wrapper scripts checked into the repository. Why can't we just use a tool that everyone just runs the latest version of?

Also, gradle uses a turing-complete language for its configuration. This leads to large config drift across different projects. Eventually everything becomes customized and cannot be updated programmatically. Every project is different. Using a single declarative JSON and "convention over configuration" file solves that.

It's 2026 and I still have to do weird things to
- get a list of dependencies with known CVEs / other vulnerabilities
- have a machine-readable list of dependencies + licenses needed for some random enterprise auditing person

These things aren't that hard and can be solved by the package manager. npm, cargo and uv show how it can be done.

The entire Java tooling seems to be centered around the experience in an IDE that is built by a single vendor. It's good, but I get annoyed pretty fast when I try to do something in some other editor.

Using maven or gradle is somehow extremely cumbersome to use in multi-stage Docker builds. Cappu aims to improve that by offering a global cache directory as well as a lockfile. Everything should be as easy as shown below.

## Usage
```sh
# create a new project with cappu.json in $PWD (similar to npm init)
cappu init

# install some dependency and add it to cappu.json
# cappu add <configuration> <group:artifact[@version]>
cappu add implementation com.google.code.gson:gson@2.14.0

# install dependencies from the lockfile (similar to npm ci)
cappu install

# build project
cappu compile

# run tests, see examples/junit-app
cappu test
```

### Other Stuff
```sh
# check dependencies for known CVEs. Exits with != 0 if there are some. You can use that in CI
cappu audit

cappu search # look up a maven package

cappu verify # check installed dependencies against their checksum, reinstall if mismatch

cappu self-upgrade # upgrade cappu binary to latest version

# get something you can forward to that one compliance person that desperately needs a list of all project dependencies + licenses
cappu licenses # optional --json
```

### LSP Server
Run this in the root of your project (so that it sees `src/main/java` under exactly that path).
```sh
cappu lsp
# optional --port if you need LSP via TCP
```
You don't have to have a `cappu.json` config as long as your project uses the common paths. However, if you want the LSP server to be able to resolve your dependencies, you should probably add a config file. You can also configure the LSP server in that config file.

### Use in Docker
Having a deterministic build + docker-managed cache is as simple as:
```Dockerfile
FROM your:base AS build
    WORKDIR /code
    COPY --from=ghcr.io/nikeee/cappu:latest /cappu /cappu # get the binary
    RUN --mount=type=bind,source=cappu.json,target=cappu.json \
        --mount=type=bind,source=cappu-lock.json,target=cappu-lock.json \
        --mount=type=cache,target=/root/.cache/cappu \
        /cappu install
    COPY ./ ./
    RUN /cappu compile # jar written to dist/

FROM eclipse-temurin:25-alpine
    COPY --from=build /code/dist/myapp.jar /app.jar
    ENTRYPOINT ["java", "-jar", "/app.jar"]
```
Don't forget to `.dockerignore` the `.cappu` dir.

#### Configure JDK
Tired of clicking through an IDE to configure some JDKs? Configure your JDK of choice in `cappu.json`:
```jsonc
{
    "jdk": "temurin-21", // supports temurin-X and corretto-X
}
```
`cappu install` will download and store the JDK to a global cache dir and use it for compilation. If you don't do that, cappu will use whatever `javac` in your `PATH` points to.

#### The History behind this Project
I didn't want to just say "build a java compiler and LSP server, make no mistakes". You can see most of my prompts in PROMPTS.md.

The result is a Java LSP server. Also a compiler built on the same foundation, just like the TypeScript compiler. The compiler is entirely optional and an even crazier experiment than this toolchain itself.

You probably shouldn't use this. I haven't read most of the source, since this is an experiment. **This readme is the only file that wasn't edited by AI.**

### Legal Notice
This project is not affiliated with Java, Oracle or similar entities.
