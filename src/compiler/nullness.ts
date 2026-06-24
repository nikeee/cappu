// Reading jspecify nullness annotations (@Nullable / @NonNull / @NullMarked /
// @NullUnmarked, https://jspecify.dev/docs/spec/) off a declaration, and deciding
// whether a *target* position (a parameter, return, field or local) is non-null.
// The checker uses these to warn when a possibly-null value reaches a non-null
// position. This is purely syntactic: declared nullness is read, never narrowed
// (no flow analysis), and generic/array-component nullness is out of scope.

import {
  type Annotation,
  type FieldDeclaration,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type ModifierLike,
  type Node,
  type NodeArray,
  type Parameter,
  type RecordComponent,
  type SourceFile,
  SyntaxKind,
} from "./types.ts";
import { entityNameToString } from "./utilities.ts";

/** The three states a target position can be in; "unknown" stays silent. */
export type Nullness = "nonNull" | "nullable" | "unknown";

/** The nullness config (FQDN lists), as it comes from cappu.json. */
export interface NullnessOptions {
  readonly enabled: boolean;
  readonly nullableAnnotations: readonly string[];
  readonly nonNullAnnotations: readonly string[];
  readonly nullMarkedAnnotations: readonly string[];
  readonly nullUnmarkedAnnotations: readonly string[];
}

/** Annotation simple-name sets the checker matches against (resolved once). */
export interface NullnessAnnotations {
  readonly nullable: ReadonlySet<string>;
  readonly nonNull: ReadonlySet<string>;
  readonly nullMarked: ReadonlySet<string>;
  readonly nullUnmarked: ReadonlySet<string>;
}

// Match by simple name so both `@Nullable` and `@org.jspecify.annotations.Nullable`
// hit the same configured entry (the same trick readDeprecation uses).
const simpleName = (qualified: string): string => qualified.replace(/^.*\./, "");

export function resolveNullnessAnnotations(options: NullnessOptions): NullnessAnnotations {
  const set = (xs: readonly string[]): ReadonlySet<string> => new Set(xs.map(simpleName));
  return {
    nullable: set(options.nullableAnnotations),
    nonNull: set(options.nonNullAnnotations),
    nullMarked: set(options.nullMarkedAnnotations),
    nullUnmarked: set(options.nullUnmarkedAnnotations),
  };
}

// A declaration's annotations live on `.modifiers` (types, methods, params,
// fields, locals) or on `.annotations` (package/module declarations).
function annotationsOf(node: Node): readonly Node[] {
  const n = node as { modifiers?: NodeArray<ModifierLike>; annotations?: NodeArray<Annotation> };
  return n.modifiers ?? n.annotations ?? [];
}

function hasAnnotation(node: Node | undefined, names: ReadonlySet<string>): boolean {
  if (!node) return false;
  for (const m of annotationsOf(node)) {
    if (m.kind !== SyntaxKind.Annotation) continue;
    if (names.has(simpleName(entityNameToString((m as Annotation).typeName)))) return true;
  }
  return false;
}

// The node carrying the modifiers + type for a symbol's declaration. A field or
// local's annotation sits on the enclosing declaration, not the VariableDeclarator.
export function carrierOf(decl: Node | undefined): Node | undefined {
  if (decl?.kind === SyntaxKind.VariableDeclarator) return decl.parent;
  return decl;
}

function typeNodeOf(carrier: Node): Node | undefined {
  switch (carrier.kind) {
    case SyntaxKind.MethodDeclaration:
      return (carrier as MethodDeclaration).returnType;
    case SyntaxKind.Parameter:
      return (carrier as Parameter).type;
    case SyntaxKind.FieldDeclaration:
      return (carrier as FieldDeclaration).type;
    case SyntaxKind.LocalVariableDeclarationStatement:
      return (carrier as LocalVariableDeclarationStatement).type;
    case SyntaxKind.RecordComponent:
      return (carrier as RecordComponent).type;
    default:
      return undefined;
  }
}

// Only reference types carry nullness; a primitive (or an unresolved `var`) never
// does. Arrays are reference types (the variable itself, not its elements).
function isReferenceType(type: Node | undefined): boolean {
  return type?.kind === SyntaxKind.TypeReference || type?.kind === SyntaxKind.ArrayType;
}

// Is `node` inside a @NullMarked scope? The nearest enclosing @NullMarked /
// @NullUnmarked on the declaration, an enclosing type, or this file's package
// declaration wins. Cross-file package-info.java is not consulted.
function isNullMarked(node: Node, a: NullnessAnnotations): boolean {
  for (let n: Node | undefined = node; n; n = n.parent) {
    if (hasAnnotation(n, a.nullUnmarked)) return false;
    if (hasAnnotation(n, a.nullMarked)) return true;
    if (n.kind === SyntaxKind.SourceFile) {
      const pkg = (n as SourceFile).packageDeclaration;
      if (hasAnnotation(pkg, a.nullUnmarked)) return false;
      if (hasAnnotation(pkg, a.nullMarked)) return true;
    }
  }
  return false;
}

/**
 * The declared nullness of a target position (the carrier node holding the
 * modifiers + type). Explicit @Nullable/@NonNull wins; otherwise a reference type
 * in a @NullMarked scope is non-null; everything else is unknown (silent).
 */
export function targetNullness(carrier: Node | undefined, a: NullnessAnnotations): Nullness {
  if (!carrier) return "unknown";
  if (hasAnnotation(carrier, a.nullable)) return "nullable";
  if (hasAnnotation(carrier, a.nonNull)) return "nonNull";
  if (!isReferenceType(typeNodeOf(carrier))) return "unknown";
  return isNullMarked(carrier, a) ? "nonNull" : "unknown";
}
