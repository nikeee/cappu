// Render compile/test diagnostics to stderr as `file:line:col code: severity: message`,
// shared by the compile and test commands.

import type { CompileDiagnostic } from "../compiler/compiler.ts";

export function renderDiagnostics(diagnostics: readonly CompileDiagnostic[]): void {
  for (const d of diagnostics) {
    const location = d.file
      ? `${d.file}:${d.line}${d.column !== undefined ? `:${d.column}` : ""}: `
      : "";
    const code = d.code !== undefined ? ` ${d.code}` : "";
    process.stderr.write(`${location}${d.severity}${code}: ${d.message}\n`);
  }
}
