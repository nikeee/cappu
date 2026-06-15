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
