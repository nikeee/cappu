# cappu

```diff
- warning -
This is an entirely vibe-coded project that acts as an experiment. I wanted to know how far I can get by steering an LLM under certain conditions. Plus I was annoyed by the Java LSP plugin for VSC. :(
```

The conditions are: The task itself be specced out by some existing spec (**"what to do"**, defined by the Java Language Spec), as well as the archicture (**"how to do it"**, defined the TypeScript compiler).

#### The Process
I didn't want to just say "build a java compiler and LSP server". So this is what I did:
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

The result is a Java-compatible compiler as well as an LSP server built on the same foundation, just like the TypeScript compiler. `.class` file baselines are verified by comparing to actual `javac` output.

You probalby shouldn't use this. **This readme is the only file that wasn't edited by AI.**


### Legal Notice
This project is not affiliated with Java, Oracle or similar entities.
