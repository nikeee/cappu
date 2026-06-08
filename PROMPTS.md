# Prompts

A verbatim record of the prompts driving this work, oldest first, kept so the
typed intent is traceable. New prompts are appended here, and each is also added
verbatim to the bottom of the commit message(s) it produced.

Verbatim - including typos - is intentional.

## Earlier sessions (parser/binder/checker/LSP, then the compiler backend)

Order within this group is approximate (reconstructed from the working summary).

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

## Current session

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
