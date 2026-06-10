# Prompts

A verbatim record of the prompts driving this work, oldest first, kept so the
typed intent is traceable. New prompts are appended here with a timestamp, and
each is also added verbatim to the bottom of the commit message(s) it produced.

Verbatim - including typos - is intentional. Timestamps are `YYYY-MM-DD HH:MM`
(local). Entries marked "(logged ...)" were backfilled in one pass, so they
carry the time they were recorded, not the exact time they were typed; per-prompt
timestamps are captured live from here on.

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
