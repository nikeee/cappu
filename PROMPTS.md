# Prompts

A verbatim record of the prompts driving this work, oldest first, kept so the
typed intent is traceable. New prompts are appended here with a timestamp, and
each is also added verbatim to the bottom of the commit message(s) it produced.

Verbatim - including typos - is intentional. Timestamps are `YYYY-MM-DD HH:MM`
(local). Entries marked "(logged ...)" were backfilled in one pass, so they
carry the time they were recorded, not the exact time they were typed; per-prompt
timestamps are captured live from here on.

## The Beginning
In general, this is what I did:
The task itself is specced out by some existing spec (**"what to do"**, defined by the Java Language Spec), as well as the archicture (**"how to do it"**, defined the TypeScript compiler).
1. Manually created the project's foundation (setting up linters etc)
2. Cloned TSC repo and looked up the latest Java Language Specification
3. Told the AI to "look at TSC and at the JLS and implement a error-tolerant parser that uses the same parsing architecture as TSC, but for JLS, with the intention of using it in a LSP server later".
4. Gave instructions to add binding and checker steps
5. Added langserver-node and let it wire up the LSP
6. Generated basic baseline refences with diff comparisons to keep track of symbol resolving and stuff
7. Let it add some real-life code from popular projects to the hover tests etc
8. While it was at it, I tested the LSP server
9. "Do now an emitter step which generates .class files. Use javac and compare the outputs on an opcode level"
10. "Take my github java project and use it as a reference"
11. "Now use a really big project and use it as a reference"

The remaining prompts are here for transparency reasons. They should also be present in the commit messages.

## Earlier sessions (parser/binder/checker/LSP, then the compiler backend)

Order within this group is approximate (reconstructed from the working summary);
no timestamps were captured.

- cover all of them
- first, look for refactoring opportunities. Add more tests with real code on a binary level. make sure float handling edge cases dont cinflict with stuff that JS/TS does with floats. then do switch and ternary
- clean up some duplicate stuff. do some meaningful refactorings. After that support lambdas and nested classes. They probably emit multiple .class files? that ones with dollars. also include them in the baseline tests
- i just merged something into this branch. keep in mind
- continue
- cover all of them
- keep on
- yes
- former
- as long as the output of javac and our code is identical when normalized, that counts as byte-wisde identical. Continue with a) and go on from there
- now take some time to find refactoring opportunities to simplify some stuff. Take common operations in the spec into account. Find duplicate and consider unifying it. Keep it simple. Sometimes it is better not to unify something.
- make sure our output is 1:1 exaclty bytewise identical with the javac outputted files. optimize the test harness for more git submodules. We're going to add more projects later
- look at the bytecode enums and check if there are some we don't support yet. evaluate if it is meaningful to support/emit them
- if youre done with whatever you are doing, clone this repository (it is mine): https://github.com/nikeee/graph-engine as a sub-mobule. use the java files in this project to generate .class baselines using javac and compare the compiler output
- explain javac -p
- -c , sorry
- go though every type annotation and check if it would be appropriate to move the inline type (probably object typey) to their own type talias. be snsible. check if it can add some sensible de-duplication
- go through the entire JLS and annotate parts of the code where they implement the respective part of the spec
- use javadoc
- use jsdoc

## Current session (logged 2026-06-08 12:41)

1. yes
2. yes
3. what are your opinions on other reference projects for class files? should contain a lot of code as a sub module. How about some spring boot project?
4. add TheAlgorithms/Java and resume try-with-resources
5. also add commons lang3
6. next
7. go through the source and add TODO comments where needed. reference JLS sections if possible
8. yes, do it.
9. when you're done, speed up the test suite by generating the reference class files of the corpus projects to some temporary directory and compare with them. Or save the normalized decompiled class files for that. Asy they are plain-text, we may also just chekc them into the source tree.
10. now, create list of stuff that is missing (feature or conformance gaps) and write them do TODO.md. Use that list to keep track of what we need to do. also update the list after every feature. then continue wuth synchronized, assert and the remaining stuff
11. write some detailed instructions on how to run the test suite to CLAUDE.md (UPDATE_BASELINES etc)
12. tidy up the test infrastructure. move the test corpus and the other reference fixtures to a common directory. maybe with sub-directories. structure the dirs based on parser reference baselines, language-service (completion, hover, etc) and emitter tests. Keep the .test.ts files where they are
13. is it possible to add something to the submodules spec, so we only clone .java files from the sub module? If so, do it
14. save all (yes, all of them) of my past and future prompts to PROMPTS.md. I want to keep track of what i was typing. Maybe also add them verbatim to the bottom part of the commit messages you generate.

## Live (timestamped)

- 2026-06-08 12:41 — also append the prompt timestamp to the verbatim record in PROMPTS.md
- 2026-06-08 12:49 — you see the TypeScript repo in this project? take it off the gitignore and add it back as a submodule
- 2026-06-08 13:00 — continue
- 2026-06-08 13:20 — I now see that "emits without crashing" only checks if the checker reports an emit error. Thats good, but i also want that the class files are checked against javac generate bytecode
- 2026-06-08 13:45 — go through all corpus projects and extract some emitter test cases that are not covered yet
- 2026-06-08 13:50 — are there some popular projects that don't have any dependencies, so we can add them as a corpus baseline and compile the entire project?
- 2026-06-08 14:05 — continue
- 2026-06-08 14:15 — continue
- 2026-06-08 17:05 — make sure the tests pass
- 2026-06-08 17:15 — continue
- 2026-06-08 17:17 — continue
- 2026-06-08 17:32 — make sure the language service parts work well with the new local classes.
- 2026-06-08 17:38 — make sure the language service parts work well with the new local classes.
- 2026-06-08 17:49 — in that order
- 2026-06-08 18:26 — continue
- 2026-06-08 18:43 — continue
- 2026-06-08 23:48 — continue
- 2026-06-08 23:51 — continue
- 2026-06-09 00:02 — continue
- 2026-06-09 00:11 — continue
- 2026-06-09 00:24 — continue
- 2026-06-09 00:47 — continue
- 2026-06-09 01:05 — continue
- 2026-06-09 01:34 — continue
- 2026-06-09 01:48 — continue
- 2026-06-09 01:58 — continue
- 2026-06-09 02:10 — continue
- 2026-06-09 02:24 — continue
- 2026-06-09 02:24 — continue
- 2026-06-09 02:38 — find some more real-life projects that dont have any dependencies and have a lot of code. Use sub-modules just like with thte other. add them to the tests and compare the bytecode output with javac reference
- 2026-06-09 03:05 — continue
- 2026-06-09 03:30 — continue
- 2026-06-09 03:48 — continue
- 2026-06-09 04:02 — continue
- 2026-06-09 04:18 — continue
- 2026-06-09 04:30 — continue
- 2026-06-09 04:42 — continue
- 2026-06-09 04:55 — add more corpus projects
- 2026-06-09 05:10 — continue
- 2026-06-09 05:25 — continue
- 2026-06-09 05:40 — keep probing / refactor / code smells / bugs+JLS / add bytecode tests
- 2026-06-09 06:10 — probe / continue
- 2026-06-09 06:35 — continue (final corpus baseline regen)
- 2026-06-09 06:50 — cli flags question; yargs ugly; server import side effect weird; bug fixes
- 2026-06-09 07:05 — continue
- 2026-06-10 00:30 — add the build artifact to the job output of cd.yaml
- 2026-06-10 00:30 — split the build step into multiple npm scripts for all major platforms and add their respective binaries to the build output
- 2026-06-10 09:05 — continue with type erasure. check for bugs in checker and emitter. add cases. what else is needed for a good compiler and/or lsp?
- 2026-06-10 09:40 — check if you can do some performance optimizations. keep in mind that the architecture of the code has to be similar to the one that typescripts compiler uses
- 2026-06-10 10:10 — continue
- 2026-06-10 10:45 — continue (corpus regen after erasure/Signature)
- 2026-06-10 11:20 — continue / check whats missing in the jdk stub
- 2026-06-10 12:05 — improve resolution on hover and other LSP tests.
- 2026-06-10 13:40 — continue (commit regen baselines)
- 2026-06-10 14:00 — continue
- 2026-06-10 14:30 — do 1. then 2 (inlay hints for parameters passed an expression; var hints, configurable). 3 and 4 sound good, too.
- 2026-06-10 15:10 — add support for code lenses, which show the number of references to the method or class. are there other meaningful items to put in a code lens?
- 2026-06-10 15:30 — add "N implementations"
- 2026-06-10 15:55 — continue / add classpath + cappu.config.json (jsonc via comment-json), compiler+lsp sections
- 2026-06-10 16:40 — add classpath lookup + cappu.config.json (jsonc, comment-json), compilerOptions/lspOptions, default $PWD or --config, compiler and lsp
- 2026-06-10 17:05 — sort all ts/js imports: node: first, then 3rd party and then internal modules
- 2026-06-10 17:20 — rename cappu.config.json to cappu.json
- 2026-06-10 17:40 — now do the natural follow ups
- 2026-06-10 17:55 — use latest zod for config file validation and typing
- 2026-06-10 18:10 — add a "cappu init" command that bootstraps an initial config file
- 2026-06-10 18:25 — is there another way to expose an LSP server? network/websockets? add a cli flag
- 2026-06-10 18:40 — use top-level await in cli.ts; tcp.once -> await once from node events
- 2026-06-10 18:55 — introduce branded types pattern; make a list of usage opportunities
- 2026-06-10 19:27 — after applying the branded types pattern, go through the entire source and look for patterns that can be replaced by node's APIs (like the once-eventemitter-helper from before). Maybe consider looking at the node.js 26 docs for that.
- 2026-06-10 19:30 — ConstantPool still has some missed opportunities for branded types. e.g. utf8(). Also make the parameters use a branded type as well, so we can get more type-safe across the entire emitting process
- 2026-06-10 19:43 — go through all comments and check if they correctly describe the behaviour they are next to. Then do the brand candidates 1-4
- 2026-06-11 00:36 — the vscode lsp extension currently does not start. fix it.
- 2026-06-11 00:36 — you don't have to use template string literal types. this may be correct, but too impractical for our use. maybe a simple branded type suffices
- 2026-06-11 00:36 — also use a string literal union for primitive descriptors
- 2026-06-11 00:53 — you've got some type annotations like: const WRAPPER: Record<string, string> = ... Dont do that. use TS's "satisfies" or "as const" operator, so the type checker can do better code elimination
- 2026-06-11 00:57 — refactor runCompile so it returns the array of diagnostics in the error case. the calling function is responsible for printing. Make sure the entire compile pipeline doesnt print anything. Maybe also move the files length check outside of runCompile. Make sure the returned diagnostics contain parser, binder and checker diagnostics. If the compiler doesnt currently check, add a flag that defaults to true that indicates if the compiler should also type-check. resolve the TODOs added in commit 3ffd69a.
- 2026-06-11 00:58 — we want to add some package management capabilities now. add some entry to the cappuConfig, configuring "packageSources" with an array of strings, where we can add maven central and stuff. We also want to default to an array that only contains maven central { "packageSources": [] } don't do anything more for now.
- 2026-06-11 01:00 — the compiler and lsp shouldnt panic if the classPath or sourcePath directories dont exist. jsut treat them as empty. if there is a cappu.json present, print a warning that it was not found. Don't print a warning if the user did not invoke it with a presnet cappu.json
- 2026-06-11 01:04 — add a compile flag that makes `cappu compile` also compile using javac, compares the bytecode output and exits with an error when they don't equal. something like --validate. use javac from $PATH for now. Maybe add an option to cappu.json where the user can specify a different javac.
- 2026-06-11 01:09 — Implement an internal api that is not connected to any of the remaining code base. it can be in a sub-directory of "src" that only contains source for that matter. it should be able to resolve and search dependencies from a set of package sources. It should be able to resolve the transitive dependencies. Add tests.
- 2026-06-11 01:09 — add other common repository sources to the default sources. maybe the ones of gradle and maven
- 2026-06-11 01:09 — keep in mind to use branded types in new code as well
- 2026-06-11 01:17 — search for more opportunities to use branded types. for example, in the parser or checker.
- 2026-06-11 01:17 — fix stuff that broke during the last feature additions and refactorings. for example, the vscode-extension broke. make sure you've got everything. be strict
- 2026-06-11 08:10 — review the entire code until now. is there anything that could be more compliant to the JLS? maybe do a refactoring of duplicate code. keep in mind that some duplication is good
- 2026-06-11 08:12 — check if we can do some things simpler. use latest node.js' APIs as well es javascript features and APIs if meaningful.
- 2026-06-11 09:22 — do 2, 3 and 5. well do package management later
- 2026-06-11 13:01 — would it be good to have sourcePaths compile the sources, instead of just building them? Would be the option for the cli argument.
- 2026-06-11 13:01 — now that we can search and download dependencies, add a section to cappu.json for dependencies. the keys under dependencies sould be similar to what gradle supports: { "dependencies": { "api": {}, "implementation": { "com.google.code.gson:gson": "2.14.0" } } }
- 2026-06-11 13:01 — now that we can search and download dependencies and have a dependencies section in the cappu.json, add the command `cappu install` that downloads all dependencies (and transitive dependencies) in the dependencies array. we only support "implementation" and "api" for now. Put the jars into the default lib/classes directory.
- 2026-06-11 13:07 — group sources files into folders. every cli command hsould have their own file. language service stuff shoudl be in their own directory (hover, code actions, completions etc). compiler-related stuff like parser, scanner, checker, binder and emitter should be in one directory
- 2026-06-11 13:17 — i get this error: ENOENT: no such file or directory, open '/home/nikeee/Projects/other/javalsp/dist/lib/classes' path: "/home/nikeee/Projects/other/javalsp/dist/lib/classes<NUL>", syscall: "open", errno: -2, code: "ENOENT"
- 2026-06-11 13:20 — add a cli command to cappu that looks like this: `cappu add implementation com.google.code.gson:gson@2.14.0`. That command should add the entry to the respective dependencies entry and download the dependency (and the respective dependencies) using the same algorithm as `cappu install`
- 2026-06-11 13:35 — add support for when the user does not add a version number to the cappu add command. Or a partial version number. it sould take the latest possible version that is compatible with the already installed dependencies. Maybe we'd need a lock file? i dont know you tell me
- 2026-06-11 13:39 — cappu install should only respect the cappu.lock.json
- 2026-06-11 13:39 — dont wirte the schema file on cappu init. add a flag to the cappu init: --with-schema that writes that file
- 2026-06-11 20:00 — add a code lens support for cappu.json's dependencies. check if each dependency has a newer version available. if so, display the new version above the dependency.
- 2026-06-11 20:28 — resolve https://github.com/nikeee/cappu/issues/3
- 2026-06-11 20:37 — resolve https://github.com/nikeee/cappu/issues/5
- 2026-06-11 20:39 — after that, resolve https://github.com/nikeee/cappu/issues/4
- 2026-06-11 21:03 — resolve https://github.com/nikeee/cappu/issues/2 maybe use a different checksum or something
- 2026-06-11 21:09 — resolve https://github.com/nikeee/cappu/issues/1 after that
- 2026-06-11 21:16 — find consellations that could lead to infinite loops and fix them. add a regression test.
- 2026-06-12 00:58 — add tests for that
- 2026-06-12 00:59 — in the next commit: rename cappu.lock.json -> cappu-lock.json. also make the lockfile use plain JSON.parse/stringify, not jsonc. its generated and not meanto to be edited by hand
- 2026-06-12 01:01 — next: add tests with cyclic inheritance and cyclic generics (direct and indirect). the tests should verify that we dont get infinite loops. test for hovers, checker and resolver.
- 2026-06-12 01:11 — in the next commit: there is no diagnostic when calling a method without parameters: [sora/Main lol() example] the () in lol() should be reported as invalid number of parameters. look out for similar issues
- 2026-06-12 01:14 — in the next commit (or multiple): introduce a global package store in the users home dir or xdg config dir (whatever fits best). Some ~/.cappu/packages where the dependencies will be downloaded into. install command checks if they are already there and just copes them from there. dir layout can be something like ~/.cappu/packages/com/google/code/gson/gson/2.14.0.jar for com.google.code.gson:gson@2.14.0. maybe this doesnt work because it can contian invalid path chars or stuff like a.b:c@1 conflicts with a.b.c:d@1
- 2026-06-12 01:18 — after that, resolve https://github.com/nikeee/cappu/issues/11
- 2026-06-12 01:20 — after that, resolve https://github.com/nikeee/cappu/issues/10
- 2026-06-12 01:26 — after that, resolve https://github.com/nikeee/cappu/issues/9
- 2026-06-12 01:30 — add a mode/flag to `cappu compile` that uses the configured javac exclusively for compilation and emit. No compilation using our own compiler.
- 2026-06-12 01:35 — now look for good refactoring opportunities and features gaps. fix them. maybe improve performance.
- 2026-06-12 09:36 — ok. do the test matrix for 1. then do 2, but you should probably use a proper xml parser now (like fast-xml-parser) instead of regexes.. Then do 3 with capu search and cappu add (no cappu update for now). Then 4, 5 and 6. All in separate commits
- 2026-06-12 09:46 — for the pom resolution, add tests that test the cases you've done above (guava, httpclient5 etc) in a separate commit
- 2026-06-12 09:51 — use URL and URLSearchParams to build a request URL. Change that for every http request that is made. except the ones that only have a static url
- 2026-06-12 14:23 — do 1, 2, 4 and resolve this issue https://github.com/nikeee/cappu/issues/15. Use commits for every bite
- 2026-06-12 15:26 — add a colorful, modern-looking progress bar for downloading in `cappu install`. You can use a popular library for that.
- 2026-06-12 15:26 — use the new node.js api for styling ansi terminal text (from utils package) instead of picocolors
- 2026-06-12 15:32 — add support for NO_COLOR env var that disables color output as well as progress bars
- 2026-06-12 15:40 — take a step back and go through every file. check if there is code that uses outdated node.js/js patterns that can be replaces by some builtin. validate the architecture.
- 2026-06-12 19:24 — commit and then resolve issue https://github.com/nikeee/cappu/issues/8
- 2026-06-12 19:24 — https://github.com/nikeee/cappu/issues/15 re-surfaced. fix it. even if it means that you have to add a new feature
- 2026-06-12 19:53 — this is a big one: https://github.com/nikeee/cappu/issues/7 make a plan beforehand and giv esome architctual decisions. split things into packages to keep domains separateed
- 2026-06-12 21:04 — ignore lombok,we dont support that. maybe implement https://github.com/nikeee/cappu/issues/17 before this. add an example directory with several example projects that use cappu. for example, some 1-file project that uses gson and mapstruct.verify that the example works end-to-end. maybe even add this as a test
- 2026-06-12 21:04 — phase d: https://github.com/nikeee/cappu/issues/16. You dont have to implement it in this plan, but you can take it into account for the future
- 2026-06-13 00:52 — when youre done: check the source and hoist any inline-require or imports. every import has to be at the top of the file. Also, no CJS. only ESM.
- 2026-06-13 01:16 — now implement cappu test. also include an example in the examples dir. also end-to-end-test the example
- 2026-06-13 01:30 — after that, add a compilerOptions.release flag. maybe that is not the best name and there is a better one
- 2026-06-13 01:30 — make a plan for cappu test first
- 2026-06-15 16:09 — is https://github.com/nikeee/cappu/issues/7 solved? double-check and review corresponding code
- 2026-06-15 16:09 — bun does not support this option: fs.glob does not support options.withFileTypes yet [...] maybe we need to find a workaround
- 2026-06-15 16:09 — make comimt closing the issue / same for https://github.com/nikeee/cappu/issues/16
- 2026-06-15 16:57 — implement the self-upgrade. use the build artifacts from the latest CD.yaml run on github. we dont need any release support for now
- 2026-06-15 17:59 — when there is no lock-file, there is no progress bar or other indicator that something is happening on cappu install. I think its in the resolving stage. maybe add some indicator(or even a progress bar?) do indicate that
- 2026-06-15 18:19 — add a "cappu cache clean" command that nukes the local global cache store
- 2026-06-15 22:17 — then do the pom caching
- 2026-06-15 22:40 — fix 1,2,3,4,5,6,8. after that we check the rest
- 2026-06-15 22:47 — actually check the sha sums in the lockfile against the jars currently installed in the lib dir
- 2026-06-15 23:04 — is there a source for CVEs of java artifacts? We want to build something similar to "npm audit" (without fix). Also build an internal API for that (in a separate folder).
- 2026-06-15 23:04 — group severity similar to npm, with colors
- 2026-06-15 23:12 — can we make "install" or "add" use more parallelism for downloads? Maybe take benefit of the lockfile?
- 2026-06-15 23:19 — next, move the default lib/classes dir for the dependencies to the .cappu dir
- 2026-06-16 08:58 — only print the installed dependencies when using --verbose. Otherwise, just print the number of installed packages per caterogy
- 2026-06-16 09:04 — go through all configs examples and comments and check whether they are (still) valid, work and describe whats going on
- 2026-06-16 09:36 — cappu audit should also check transitive dependencies. similar experience to npm audit. print the affected dependency tree if they encounter something.
- 2026-06-16 09:39 — next, add an example for cappu audit where it raises a concern. add that to the end-to-end tests
- 2026-06-16 09:45 — after that, add a cappu update command that bumps dependencies if possible. make sure the dependency tree is still valid (compatible versions).
- 2026-06-16 09:49 — after that, print the time taken at cappu install, verify, add, etc / make the output of cappu install more colorful
- 2026-06-16 11:42 — add an end-to-end test for cappu update
- 2026-06-16 14:09 — node.js 26 comes with more SEA support. is it possible to migrate this to SEA? maybe tsdown has an option for this. look it up in the docs / do it and drop macos x64
- 2026-06-16 14:09 — use dist dir for the new binaries
- 2026-06-16 14:09 — make the tsdown config use an expression instead of an if-statement
- 2026-06-16 14:21 — have you removed the workaround for bun? are there other workarounds for bun compat in the source?
- 2026-06-16 14:24 — what about the resources directory? for tests and application? Add a test that uses these with cappu test and cappu compile to the end-to-end-tests in the example directory
- 2026-06-16 14:31 — next, look at every comment and check if its still valid. Also check if it is unnecessary and can be removed
- 2026-06-16 14:51 — where does the cached metadata reside on disk?
- 2026-06-16 14:51 — show my my local metadata
- 2026-06-16 14:51 — ive run cappu install in the examples/audit-app and there is no metadata
- 2026-06-16 14:51 — is it possible to add the license of the package to the metadata?
- 2026-06-16 14:51 — is it an spdx identifier?
- 2026-06-16 14:51 — Consolidate the three comment-audit Explore agents' findings (stale/wrong + redundant comments), apply the fixes, run lint/typecheck/tests, and commit.
- 2026-06-16 15:08 — implement license field like #1. Add a best-effort-mapping to a separate field "licenseNormalized". Always log licenses if they could not be mapped to an spdx identifier
- 2026-06-16 15:08 — draw in the most common packages and parse their licenses, add them to the default mapping
- 2026-06-16 15:08 — when you are done, add tests for the common cases, including end-to-end
- 2026-06-16 15:08 — after that, add a command `cappu licenses` that prints all used dependencies and their used license. Also add a --json flag that prints it machine readable
- 2026-06-16 15:08 — also take common license URLs into account for normalization
- 2026-06-16 15:20 — add a progress bar to the self-upgrade. also make it colorful
- 2026-06-16 15:28 — next, add a license field to cappu.json similar to the spdx-license of npm's package.json. Only support spdx.
- 2026-06-16 15:34 — cappu audit is a little slow. can we cache something here?
- 2026-06-16 15:40 — is the vuln store really immutable? i think there can be new vulns at a later time. maybe we'd want to add a ttl of one day? check this
- 2026-06-16 19:30 — add the licenses of a package to the lock file. dont use the best-effort normalized, but the raw one
- 2026-06-16 19:30 — add a flag to the auditing to skip all cached data and do a fresh scan
- 2026-06-16 19:30 — continue
- 2026-06-16 20:28 — what is needed to make the jar of "cappu compile" to emit something that is publishable to a maven registry?
- 2026-06-16 20:28 — make a plan for that
- 2026-06-16 20:28 — version must be semver. Also add a publishing test that uses testcontainers to run a temporary maven repository and test install from that in an end-to-end test
- 2026-06-16 20:40 — use some default registry for cappu publish. Make it configurable the same way npm can use a different registry
- 2026-06-16 20:50 — next, implement `npm version major/minor/patch` command that updates the cappu.json and if in the root and inside a git repo, creates a tag
- 2026-06-16 21:05 — next: we've already introduced branded types. go through the entire project and look for opportunities to replace primitive types string/number with some branded type.
- 2026-06-16 21:20 — do all of them
- 2026-06-16 21:20 — except the DocVersion
- 2026-06-16 21:35 — next: the progress bar of cappu self-upgrade is missing units (MiB?)
- 2026-06-16 21:50 — next: cappu licenses lists a lot of "no license declared". e.g. junit:junit:4.13. is that due to missing spdx mappings? fall back to the raw license
- 2026-06-16 21:50 — add spdx identifier "New BSD License" that maps to "BSD-3-Clause" if it is correct
- 2026-06-17 09:15 — next: create an example application that starts a basic spring boot project. use latest spring boot. maybe plan before
- 2026-06-17 10:30 — lsp: "var" is not clickable to go to definition. variables defined with `var` do not count as references on some occasions
- 2026-06-17 09:32 — these spdx mappings are missing: warning: org.danilopianini:gson-extras:0.2.1: license "Apache License 2.0" has no SPDX mapping [...full warning list...]
- 2026-06-17 09:32 — `cappu rage` should open the issue tracker in your default browser
- 2026-06-17 09:32 — use ebug url from package.json. if there is non, add it
- 2026-06-17 09:32 — use switch for pre-loadCOnfig commands
- 2026-06-17 10:45 — lsp: in methods of a class, instance variables are not offered for completion if we're not qualifying using "this.". there is also no auto completion when dotting into on a member without "this."
- 2026-06-17 10:50 — add an example to examples/ that uses org.immutables
- 2026-06-17 11:05 — remove ability to configure output dir. it is always dist.
- 2026-06-17 11:10 — next, add --json for cappu audit if not yet present
- 2026-06-17 11:20 — after that: support testImplementation and other dependencies (especially in cappu add)
- 2026-06-17 11:20 — after that: chage artifact coordinates in "cappu add" to be without @ but with : for the version instead, so a user can just copy a line from some build.gradle and prepend `cappu add`
- 2026-06-17 11:25 — when you're done: lsp: when accessing a classpath resource, so getResourceAsStream is called on a "class" type (or simlar), we want to provide auto completion for the string value passed. The user should be offered a list of all valid classpath resources that we can see.
- 2026-06-17 11:35 — next: if not already done, add default mvn/gradle lib paths to cappu config paths, so the lsp can pick up the jars managed by them
- 2026-06-17 11:45 — after that: move all flags related to the internal/own compiler to a `experimentalCompiler` key: enabled (default false), failOnDegrade (default true), the javac-comparison validate mode, and whatever falls under this category
- 2026-06-17 11:55 — after that: cappu update should check for updates on transitive dependencies that satisfy the version ranges and update them in the lockfile
- 2026-06-17 11:55 — (clarified) 2, but do not bump major versions
- 2026-06-17 12:10 — can the artifact be overwritten in cappu-compile via cli? useful to steer output jar for docker builds
- 2026-06-17 12:25 — next: make cappu init ask some questions: groupId, artifactId, etc; add a -y parameter that fills in defaults (like npm init); make default output to fat-jar; ask output on init (library (jar) / application (fat-jar) / classes). use inquirer.
- 2026-06-17 12:35 — remove all experimental compiler options from the cli options. they should only be in the cappu.json
- 2026-06-17 15:27 — doing a new session for better contexts.   Handoff for the fresh session — TODO #2–#4 (all in TODO.md, all experimental-compiler bytecode):

  - #2 InnerClasses (JVMS 4.7.6) — start here. The NestHost/NestMembers work is already done and is the closest pattern to mirror (same nested/local/anon class enumeration;
  different attribute shape + access flags + inner-name).
  - #3 RuntimeVisibleAnnotations (4.7.16) — needs annotation element-value encoding (likely the largest).
  - #4 LocalVariableTable (4.7.13) — the emitter already tracks Slots and has the LineNumberTable pattern to follow.

  Each is one focused effort: implement → UPDATE_BASELINES=1 …emitter.test.ts to regenerate the javac byte-match baselines (needs javac/javap) → commit the regenerated
  fixtures.

  Two notes from this session worth carrying over:
  - The experimentalCompiler config is now nested (compilerOptions.experimentalCompiler.{enabled,failOnDegrade,validate}) and there are no longer any experimental CLI flags
  — enable it via cappu.json.
  - node --run typecheck populates the vendored TypeScript-Go submodule, after which repo-wide node --run lint/format also scan it and warn; use oxlint src/ to lint just our
  code.

- 2026-06-17 15:28 — plan issue https://github.com/nikeee/cappu/issues/18
- 2026-06-18 00:21 — next: scan all ts files and look for more branded types opportunities
- 2026-06-18 00:36 — also update CLAUDE.md so it includes instructions for the to-go codebase. Also add that every feature should be implemented in both code bases. And when editing both code bases, always look out for diverging behaviour
- 2026-06-18 00:36 — then continue with next milestone
- 2026-06-18 00:36 — commit. just use go fmt standard tooling. rename to-go dir to togo (its simpler)
- 2026-06-18 00:36 — dont branch. there is some work going on in the ts version
- 2026-06-18 09:15 — continue (milestone 2: Maven resolution engine + licenses)
- 2026-06-18 09:25 — continue (milestone 3: install)
- 2026-06-18 09:25 — since we're knowing the schema of our lockfile, can we use an json parser that uses codegen to use a specialized parser? For perf reasons and better maintainability?
- 2026-06-18 09:33 — is it a good idea to no commit the generated easyjson files?
- 2026-06-18 09:42 — since we now have a generate step, consider doing a shcema-based pom-parser using some codegen, too. But proceed with what you were doing
- 2026-06-18 09:51 — before continuing with the next milestone (but after this one): Is there a cool library for cli argument parsing? that handwritten loop looks weird. Since we now have codegen, can we use a library that builds on top of that? Maybe we also dont have to write the entire help page manually
- 2026-06-18 09:57 — continue (milestone 5: audit)
- 2026-06-18 10:07 — continue (milestone 6: publish)
- 2026-06-18 14:26 — build the lsp first. start with the scanner, then parser, binder, checker and then the rest. do the language service features last. also port each individual test on each step
- 2026-06-18 14:26 — dont forget to follow the same pattern for porting the code as TypeSript-Go did
- 2026-06-18 20:22 — continue
- 2026-06-18 21:36 — continue
- 2026-06-18 21:41 — continue
- 2026-06-18 21:45 — continue
- 2026-06-18 21:48 — continue
- 2026-06-18 21:52 — continue
- 2026-06-18 21:58 — continue
- 2026-06-18 22:05 — continue
- 2026-06-18 21:37 — explore https://github.com/nikeee/cappu/issues/19
- 2026-06-18 21:37 — plan it. only for the ts version of cappu, not togo
- 2026-06-18 21:37 — 2 (execute the plan inline)
- 2026-06-18 21:37 — i merged main into current branch. maybe something is broken. but there is now a lot more functionality / re-evaluate methods to implement for the MCP server. Align code architecture and patterns with the merged update / continue
- 2026-06-19 02:25 — continue (deferred MCP project tools: audit, licenses, search_packages)
- 2026-06-19 02:30 — continue
- 2026-06-19 02:41 — continue
- 2026-06-19 02:55 — continue
- 2026-06-19 03:10 — continue
- 2026-06-19 03:25 — continue
- 2026-06-19 03:35 — continue
- 2026-06-19 12:34 — build everything you wouldnt skip
- 2026-06-19 12:42 — you probably have some mcp way of telling an AI to use `cappu compile` `cappu test` cli tool for things that require writing. Do that, so we don't have to implement this in MCP and the user can decide wheter they want writing operations
- 2026-06-19 12:49 — Also add a command `cappu config-schema` that just prints the jsonschema of the config file. add a note to the mcp instructions that cappujson is the config file and that the shcema can be retrieved using `cappu config-schema`.
- 2026-06-19 12:49 — update the instructions to talk like caveman
- 2026-06-19 13:54 — port all lsp features before porting mcp
- 2026-06-19 14:30 — port all 849 tests; port failing ones anyway
- 2026-06-20 16:06 — continue porting the lsp
- 2026-06-20 16:20 — port the rest. find feature gaps. make sure you really have every test
- 2026-06-20 22:52 — port the bytecode emitter (continue)
- 2026-06-21 00:00 — continue (wire cappu compile)
- 2026-06-21 01:15 — do all the remaining stuff
- 2026-06-21 13:40 — go through the entire new go (togo) code and look for logic differences to the ts version. be strict. find unsupported stuff.
- 2026-06-21 13:40 — also check if every test case in the ts version has an equivalent in the go version. count all tests.
- 2026-06-21 13:40 — fix all of these issues. then audit further
- 2026-06-21 13:40 — fix all of them. regarding offsets vs code points: do what the JLS says about that.
- 2026-06-21 14:05 — checker, then bytecode
- 2026-06-21 14:05 — continue
- 2026-06-21 18:43 — your enemy copilot ported the code from ts to go (in togo dir). Try to find things that he missed. Make sure the behaviour of both versions is exactly identical.
- 2026-06-21 19:22 — ci.yaml fails: https://github.com/nikeee/cappu/actions/runs/27902473974/job/82565141525 find out why and fix

- 2026-06-21 19:24 — resolve https://github.com/nikeee/cappu/issues/21

- 2026-06-21 20:48 — make sure we emit a diagnostic when we cant resolve a type. i just opened the mapstruct sample and asked the mcp for diagnostics and it did not return anything. but it had to, because the method that i called could not have existed.
- 2026-06-21 20:54 — cappu search org.apache.commons:commons-lang3
no packages found for 'org.apache.commons:commons-lang3'

but this works:
cappu search commons-lang3

- 2026-06-21 21:06 — look out for strings (or parts of strings) in the source that can be replaced by a reference to a constant, e.g. `./dist` as the default directory, so we dont diverge if they change
- 2026-06-21 21:06 — imeplement https://github.com/nikeee/cappu/issues/20
- 2026-06-21 21:31 — make the output of cappu search prettier and include some more info if possible
- 2026-06-21 21:39 — investigate wether we can replace some code with commonly used libraries.
- 2026-06-21 22:26 — make a plan. it should not impact startup performance or hog memory. maybe just lazy. or if it doesnt work good at all, just keep the stubs (and maybe extend them)
- 2026-06-21 22:26 — yes, auto mode and use a separate branch
- 2026-06-21 23:24 — add more tests. determine edge cases
- 2026-06-21 23:55 — cappu already has LSP and MCP. How about support debug adapter protocol? would it make sense?
- 2026-06-21 23:55 — do you mean this by java-debug? https://github.com/microsoft/vscode-java-debug maybe we can use some unit test cases from there to test our own implementation
- 2026-06-21 23:55 — add an example how to use it to the examples directory
- 2026-06-22 11:38 — fix issues and find refactoring opportunities first. fan out multiple agents
- 2026-06-22 11:50 — add more protocol tests. and an end-to-end test that uses the example
- 2026-06-22 11:57 — i see some child.once. use the event_emitter helper once() that returns a promise. look out for similar things that could be used instead. prefer builtins
- 2026-06-22 11:57 — next: move the example descriptions to a README.md file in every respective example directory
- 2026-06-22 12:00 — we're now having DAP. Do we have some options that could be placed in the cappu.json config, similar to lspOptions? Do we need some options for MCP?
- 2026-06-22 12:13 — 1
- 2026-06-22 15:05 — fix the spring boot sample we want a single jar. or war? can we support that? Do we need another output type?
- 2026-06-22 15:11 — add this as an e2e test
- 2026-06-22 15:14 — add an option for always enableAssertions
- 2026-06-22 15:21 — the go version doesnt seem to have a progress bar when downloading dependencies
- 2026-06-22 15:37 — can we make use of the Disposable symbol and using somewhere?
- 2026-06-22 15:20 — DX: after building something, we might want to print how to run the file. e.g. `java -jar dist/app.jar` after building the app jar. May only apply to applications and not libraries
- 2026-06-22 15:43 — support short identifiers for cappu add; i for "implementation"
- 2026-06-22 15:48 — cappu audit did not report any issue when calling it without .cappu dir and cleaned cache???
- 2026-06-22 15:48 — yes
- 2026-06-22 17:01 — review the entire go code for common code smells, unnecessary duplication, wrong architecture and bad patterns. your enemy copilot wrote this. be extra strict
- 2026-06-22 17:01 — continue
- 2026-06-22 17:01 — continue
- 2026-06-22 16:46 — cappu licenses has different output from --json as without
- 2026-06-22 16:46 — couldnt you just add the dependencie's raw licenses?
- 2026-06-22 16:46 — how about "licenses": [{"name":"MIT", "spdx": "MIT"}]
- 2026-06-22 16:46 — yes
- 2026-06-22 17:05 — i merged. continue
- 2026-06-22 17:20 — continue
- 2026-06-23 09:08 — any idea what would be good for this project?
- 2026-06-23 10:41 — commit on main. then next feature
- 2026-06-23 10:41 — continue
- 2026-06-23 10:47 — we want to make the repository public. cappu self-upgrade should check for the latest public github tag/release instead of CI run. Remove GH token. Add CD step and release creation and stuff
- 2026-06-23 10:48 — do it in a separate worktree in ../javalsp-publish
- 2026-06-23 10:52 — anything else?
- 2026-06-23 10:52 — do them step by step. commit automatically.
- 2026-06-23 10:59 — after all that: can the LSP/language services/checker benefit from what youve built in this session?
- 2026-06-23 14:48 — make sure the versions on the release are the go version, not the node version
- 2026-06-23 19:21 — also add finding deprecated uses to the MCP
- 2026-06-23 19:21 — excplicitly add a command that the MCP can use to get deprecated uses. you can add more information on deprecated uses. for example, the deprecation message, deprecated symbol etc
- 2026-06-23 19:21 — are there other features similar to deprecated uses that an LLM could make use of?
- 2026-06-24 11:03 — 2,3,4,5
- 2026-06-24 11:03 — continue
- 2026-06-24 11:21 — any idea what would be good for this project?
- 2026-06-24 11:21 — also add regression tests
- 2026-06-24 11:21 — continue
- 2026-06-24 15:59 — continue. commit automatically.
- 2026-06-24 16:51 — resolve https://github.com/nikeee/cappu/issues/23
- 2026-06-24 17:34 — resolve https://github.com/nikeee/cappu/issues/25
- 2026-06-24 17:34 — are you going to put the nullability as a type flag/union similar to typescripts |null?
- 2026-06-24 17:34 — maybe add this to the compiler options, not the lsp options. in compilation mode (and lsp), we should emit a warning diagnostic. maybe just start with lsp and do compiler later, since we only have an experimental compiler
- 2026-06-24 22:14 — implement https://github.com/nikeee/cappu/issues/24
- 2026-06-24 22:31 — support cross-file package-info.java and generic nullness
- 2026-06-24 22:39 — add the new go tests to ts
- 2026-06-24 23:04 — add support for flow-aware type checks. TS has some gettypeOfSymbolAtLocation. we should probably do something simiar.
- 2026-06-24 23:04 — also add an example demonstrating the null checking in the examples/dir
- 2026-06-24 23:23 — do 1 and 2. add aforementioned tests beforehand
- 2026-06-25 08:52 — make sure we pass ALL formatting tests
- 2026-06-25 08:52 — (chose: Just the cheap wins)
- 2026-06-25 08:52 — also print the number of files formatted
- 2026-06-25 08:52 — port to go version
- 2026-06-24 23:36 — merge into main. do Dereference-of-nullable and branch-merge. commit directly on main
- 2026-06-25 08:49 — in the example/, when i do shout(found); inconditionally (with found being nullable), there is no error?
- 2026-06-25 09:23 — what next?
- 2026-06-25 09:23 — 1 and 4
- 2026-06-25 09:56 — what next?
- 2026-06-25 09:56 — plan the line wrapping optimizer fpr 100% gjf compat
- 2026-06-25 09:43 — implement JEP 441 case null patterns
- 2026-06-25 17:33 — what next?
- 2026-06-25 17:33 — field narrowing would be cool, but is this thread-safe?
- 2026-06-25 17:33 — do final field narrowing, method-return narrowing, try/catch flow
- 2026-06-25 17:33 — does this feature support modern stuff like records?
- 2026-06-25 17:40 — find more test cases. ones that shoukd adhere correct nullness and ones that shouldnt. maybe even look in some reference projects
- 2026-06-25 17:42 — add a test where String x = "foo"; if(Random()) x = null; s.length;
- 2026-06-25 18:40 — fix false positives and negatives
- 2026-06-25 21:49 — print the number of files formatted (including which were changed)
- 2026-06-25 23:34 — add the equivalent of npm ls - maybe under a different name?
- 2026-06-26 01:40 — looking from a dx perspective - were trying to create a simple tool that uses convention over configuration (like vite) and want to build a workflow that is similar to uv and npm. what can we do better? what is missing? what behaves unexpected?
- 2026-06-26 01:40 — dont do A2
- 2026-06-26 01:40 — track C: no alias. pick one
- 2026-06-26 09:24 — resolve https://github.com/nikeee/cappu/issues/28
- 2026-06-26 09:24 — are there generated sources for tests?
- 2026-06-26 09:28 — resolve https://github.com/nikeee/cappu/issues/30
- 2026-06-26 10:03 — group the help texts of all commands. make them more colorful. similar to bun: Bun is a fast JavaScript runtime, package manager, bundler, and test runner. (1.3.14+0d9b296af) [...bun --help example...]
- 2026-06-26 10:03 — commit on main and push when finished
- 2026-06-27 12:55 — implement concurrent/parallel formatting of files. this should speed up formatting
- 2026-06-27 13:54 — what does quite mode do currently?
- 2026-06-27 13:54 — remove it form config
- 2026-06-27 13:54 — in a similar fashion, do https://github.com/nikeee/cappu/issues/29
- 2026-06-27 14:06 — investigate https://github.com/nikeee/cappu/issues/31
- 2026-06-27 14:14 — go through the code and investigate opportunities for de-duplication, refactorings and simplifications. also check if both sources use all sensible branded types
- 2026-06-27 14:15 — go through the formatting code and investigate opportunities for de-duplication, refactorings and simplifications
- 2026-06-27 21:44 — the foratter is not on par with gjf. make it 100% compatible. we're only at 40%. thats unacceptable.
- 2026-06-27 21:44 — did you port the gjf parser or do we still use our own parser?
- 2026-06-27 21:44 — then grind.
- 2026-06-29 10:25 — cappu install still hangs in in the grpah engine project: git clone git@github.com:nikeee/graph-engine.git (branch cappu-test)
- 2026-06-29 10:31 — make the cappu-lock.json deterministic. sort the entries alphabetically
- 2026-06-29 11:13 — ci fails https://github.com/nikeee/cappu/actions/runs/28361177399
- 2026-06-29 11:13 — commit to main
- 2026-06-29 16:28 — add some test coverage tool to the go version. use the one that everyone uses.
- 2026-06-29 16:28 — yes
- 2026-06-29 16:28 — now that you have code coverage tools, use it to find untested code paths and check for their correct bahaviour. maybe even add tests that cover their branches
- 2026-06-29 16:28 — commit between steps
- 2026-06-29 16:28 — anything else?
- 2026-06-29 16:28 — do the cheap ones
- 2026-06-29 22:39 — for cappu audit and cappu licenses: are there common formats that some tools can consume? like tap for tests or lcov for coverage?
- 2026-06-29 22:39 — yeah. implement sarif and osv for audit. well do licenses later. maybe remove "json" format form cappu audit, as it is not standardized
- 2026-06-29 22:39 — use "--format sarif", so we dann add more later
- 2026-06-30 08:33 — resolve https://github.com/nikeee/cappu/issues/35
- 2026-06-30 08:33 — I cloned bun's source to /tmp/zshtmp.J7FVfl/bun, where you can check how they are doing it
- 2026-06-30 09:01 — resolve https://github.com/nikeee/cappu/issues/36
- 2026-06-30 10:14 — add cappu vache verify
- 2026-06-30 10:14 — add _metadata to go version
- 2026-06-30 14:22 — https://github.com/nikeee/cappu/issues/31 is still an issue
- 2026-06-30 14:37 — go through the sources of the ts and go version. look for diverging behavior. fix them.
- 2026-06-30 14:37 — commit on main
- 2026-06-30 20:17 — look at the ts and go sources. spot refactoring or simplification opportunities. use builtins if they are obvious. assume you can use newest node.js (>26) and latest go (>1.26)
- 2026-06-30 20:17 — aal of them
- 2026-06-30 20:17 — continue
- 2026-07-01 09:27 — does this make sense? https://github.com/nikeee/cappu/issues/32 if so, build it and make it colorful output. it should look pretty and be informative with everything a dev usually needs to know about this dependency
- 2026-07-01 09:27 — commit when you are done
- 2026-07-01 19:13 — in cappu LSP and cappu check, add a diagnostic warning if string.format is getting called with the wrong number of arguments (and similar gotchas in similar methods)
- 2026-07-01 14:12 — in `cappu test` add a default location for some test results (if they are specified; maybe default to stdout plain/colorful text). for the test results also add other output types.
how about:
```
"testOptions": {
    "outputFormat": "junit",
    // path: default
}
"testOptions": {
    "outputFormat": "tap",
    // path: default
}
```
maybe the path should just be a directory and default to ./dist?
- 2026-07-01 14:12 — add e2e tests that check whether a valid file is emitted
- 2026-07-01 19:13 — in cappu LSP and cappu check, add a diagnostic warning if string.format is getting called with the wrong number of arguments (and similar gotchas in similar methods)
- 2026-07-01 19:39 — commit and find other use-cases
- 2026-07-01 19:11 — in the lsp, if i use a deprecated function, is it rendered as crossed-out in the client?
- 2026-07-01 19:11 — yes
- 2026-07-01 19:16 — any other special markers?
- 2026-07-01 19:16 — yes
- 2026-07-01 19:24 — do it
- 2026-07-01 19:36 — do 1 and continue with the rest
- 2026-07-01 23:29 — cappu update, self-upgrade and stuff doesnt print some status what it is actually doing. there is missing some user feedback. also no progress bar on self-upgrade
- 2026-07-01 23:29 — continue
- 2026-07-01 23:33 — make sure we support maven (or gradle?) version ranges in cappu.jsons dependencies, so we can resolve better
- 2026-07-01 23:40 — now that we support output formats in testOptions, what about test coverage reports?
- 2026-07-01 23:56 — review the entire go+ts source and check for perf gaining opportunities. do refactorings. check for correctness and for surprising behaviour
- 2026-07-02 14:28 — analyze the every sub-command and look out for unexpected, wrong or diverging behaviour. fan out agents
