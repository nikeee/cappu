# cappu

This is an entirely vibe-coded project that acts as an experiment. I wanted to know how far I can get by steering an LLM under certain conditions. Plus I was annoyed by the Java LSP plugin for VSC. :(

The conditions are: The task itself be specced out by some existing spec ("what to do", defined by the Java Language Spec), as well as the archicture ("how to do it", defined the TypeScript compiler).

So this is a Java-compatible compiler as well as an LSP server built on the same foundation, just like the TypeScript compiler. `.class` file baselines are verified by comparing to actual `javac` output.

You probalby shouldn't use this.
