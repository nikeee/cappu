# cappu

```diff
- warning -
This is an entirely vibe-coded project that acts as an experiment. I wanted to know how far I can get by steering an LLM under certain conditions. Plus I was annoyed by the Java LSP plugin for VSC. :(
```

The conditions are: The task itself be specced out by some existing spec (**"what to do"**, defined by the Java Language Spec), as well as the archicture (**"how to do it"**, defined the TypeScript compiler).

The result is a Java-compatible compiler as well as an LSP server built on the same foundation, just like the TypeScript compiler. `.class` file baselines are verified by comparing to actual `javac` output.

You probalby shouldn't use this. This readme is the only file that wasn't edited by AI.
