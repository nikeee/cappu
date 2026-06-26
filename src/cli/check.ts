// `cappu check`: type-check the project with cappu's own checker (the LSP's
// diagnostics, nikeee/cappu#30) and report them - no class files are written.
// With no files, check everything under the configured sourcePaths.

import { runCheck } from "../compiler/compiler.ts";
import type { CappuConfig } from "../config.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";
import { findSourceJavaFiles } from "../workspace.ts";

export function runCheckCommand(files: string[], config: CappuConfig): never {
  const inputs = files.length > 0 ? files : findSourceJavaFiles(config);
  if (inputs.length === 0) {
    process.stderr.write(
      "usage: cappu check <file.java> ...\n" +
        "(no files given and the configured sourcePaths contain no .java files)\n",
    );
    process.exit(2);
  }
  const diagnostics = runCheck(inputs, config);
  renderDiagnostics(diagnostics);
  process.exit(diagnostics.some(d => d.severity === "error") ? 1 : 0);
}
