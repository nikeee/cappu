// Reading the @Deprecated annotation (JLS 9.6.4.6) off a declaration, and the
// shape of a deprecated *use* reported by the checker and the MCP server.

import {
  type Annotation,
  type AnnotationArgument,
  type LiteralExpression,
  type Node,
  SyntaxKind,
} from "./types.ts";
import { entityNameToString } from "./utilities.ts";

/** The information a `@Deprecated(since=..., forRemoval=...)` annotation carries. */
export interface Deprecation {
  readonly since?: string;
  readonly forRemoval: boolean;
}

/** One use of a deprecated declaration, as surfaced to diagnostics and the MCP. */
export interface DeprecatedUse {
  /** Span of the referenced name in the source file. */
  readonly pos: number;
  readonly end: number;
  /** The referenced name (method or type). */
  readonly name: string;
  /** What kind of declaration was used. */
  readonly kind: "method" | "type";
  readonly since?: string;
  readonly forRemoval: boolean;
}

// Read a @Deprecated annotation off a declaration's modifiers, returning its
// since/forRemoval, or undefined when the declaration is not deprecated. Matches
// the annotation by simple name (the standard java.lang.Deprecated); a user type
// also named Deprecated is not distinguished, which is vanishingly rare.
export function readDeprecation(declaration: Node | undefined): Deprecation | undefined {
  const modifiers = (declaration as { modifiers?: readonly Node[] } | undefined)?.modifiers;
  for (const m of modifiers ?? []) {
    if (m.kind !== SyntaxKind.Annotation) continue;
    const ann = m as Annotation;
    if (entityNameToString(ann.typeName).replace(/^.*\./, "") !== "Deprecated") continue;
    let since: string | undefined;
    let forRemoval = false;
    for (const arg of ann.args ?? []) {
      const a = arg as AnnotationArgument;
      const name = a.name?.text ?? "value";
      if (name === "since" && a.value.kind === SyntaxKind.StringLiteral) {
        since = (a.value as LiteralExpression).value;
      } else if (name === "forRemoval" && a.value.kind === SyntaxKind.TrueKeyword) {
        forRemoval = true;
      }
    }
    return { since, forRemoval };
  }
  return undefined;
}
