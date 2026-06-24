// Reading jspecify nullness annotations (@Nullable / @NonNull / @NullMarked /
// @NullUnmarked, https://jspecify.dev/docs/spec/) off declarations and type nodes.
// The checker turns these into a nullness facet on the type model (see
// checkerTypes.ts) and warns when a possibly-null value reaches a non-null position.
// Purely syntactic: declared nullness is read, never narrowed (no flow analysis).

import {
  type Annotation,
  type ModifierLike,
  type Node,
  type NodeArray,
  SyntaxKind,
} from "./types.ts";
import { entityNameToString } from "./utilities.ts";

/** The three states a position can be in; undefined/"unknown" stays silent. */
export type Nullness = "nonNull" | "nullable";

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
// fields, locals) or `.annotations` (record/package/module/type-use nodes).
function annotationsOf(node: Node): readonly Node[] {
  const n = node as { modifiers?: NodeArray<ModifierLike>; annotations?: NodeArray<Annotation> };
  return n.modifiers ?? n.annotations ?? [];
}

/** Whether `node` carries any annotation whose simple name is in `names`. */
export function hasNullnessAnnotation(node: Node | undefined, names: ReadonlySet<string>): boolean {
  if (!node) return false;
  for (const m of annotationsOf(node)) {
    if (m.kind !== SyntaxKind.Annotation) continue;
    if (names.has(simpleName(entityNameToString((m as Annotation).typeName)))) return true;
  }
  return false;
}

function nullnessFrom(node: Node | undefined, a: NullnessAnnotations): Nullness | undefined {
  if (hasNullnessAnnotation(node, a.nullable)) return "nullable";
  if (hasNullnessAnnotation(node, a.nonNull)) return "nonNull";
  return undefined;
}

// The node carrying the modifiers for a symbol's declaration. A field or local's
// annotation sits on the enclosing declaration, not the VariableDeclarator.
export function carrierOf(decl: Node | undefined): Node | undefined {
  if (decl?.kind === SyntaxKind.VariableDeclarator) return decl.parent;
  return decl;
}

/** Nullness declared by a declaration's own modifiers (@Nullable String s). */
export function readDeclaredNullness(
  decl: Node | undefined,
  a: NullnessAnnotations,
): Nullness | undefined {
  return nullnessFrom(carrierOf(decl), a);
}

/** Nullness written as a type-use annotation on a type node (List<@Nullable T>). */
export function typeUseNullness(
  typeNode: Node | undefined,
  a: NullnessAnnotations,
): Nullness | undefined {
  return nullnessFrom(typeNode, a);
}

// Only reference types carry nullness; a primitive (or `var`) never does. Arrays
// are reference types (the variable itself, not its elements).
export function isReferenceTypeNode(type: Node | undefined): boolean {
  return type?.kind === SyntaxKind.TypeReference || type?.kind === SyntaxKind.ArrayType;
}
