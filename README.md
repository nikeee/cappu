# cappu
A Java compiler, lsp and toolchain.

```diff
- warning -
This is an entirely vibe-coded project that acts as an experiment.
I wanted to know how far I can get by steering an LLM under certain conditions.

Plus I was annoyed by the Java LSP plugin for VSC. :(
```

#### The Process
I didn't want to just say "build a java compiler and LSP server". You can see most of my prompts in PROMPTS.md.

The result is a Java-compatible compiler as well as an LSP server built on the same foundation, just like the TypeScript compiler. `.class` file baselines are verified by comparing to actual `javac` output.

You probalby shouldn't use this. **This readme is the only file that wasn't edited by AI.**


### Legal Notice
This project is not affiliated with Java, Oracle or similar entities.
