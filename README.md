# cappu
A Java compiler, lsp and toolchain. Favors convention over configuration.
```diff
- warning -
This is an entirely vibe-coded project that acts as an experiment.
I wanted to know how far I can get by steering an LLM under certain conditions.

Plus I was annoyed by the Java LSP plugin for VSC. :(
```
## Why?
I was annoyed by the Java extension for VSC that everyone uses. So I vibed my own LSP server. And then added a compiler and package manager because why not?

I loathe ./gradlew and ./mvnw with random wrapper scripts checked into the repository. Why can't we just use a tool where everyone just runs the latest version?

Also, gradle uses a turing-complete language for its configuration. This leads to large config drift across different projects. Everything becomes customized and cannot be updated programmatically. Every project is different. Using a single declarative JSON and "convention over configuration" file solves that.

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
# check dependencies for known CVEs
cappu audit

cappu search # look up a maven package

cappu verify # check installed dependencies against their checksum, reinstall if mismatch

cappu self-upgrade # upgrade cappu binary to latest version
```

### LSP Server
```sh
cappu lsp
# optional --port if you need LSP via TCP
```

#### The Process
I didn't want to just say "build a java compiler and LSP server". You can see most of my prompts in PROMPTS.md.

The result is a Java-compatible compiler as well as an LSP server built on the same foundation, just like the TypeScript compiler. `.class` file baselines are verified by comparing to actual `javac` output.

You probalby shouldn't use this. **This readme is the only file that wasn't edited by AI.**

### Legal Notice
This project is not affiliated with Java, Oracle or similar entities.
