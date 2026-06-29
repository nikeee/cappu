// Turns an agent-supplied symbol reference into engine symbols. References are
// name-addressed (agents do not have file offsets): either a type (FQN
// "com.foo.Bar" or a bare simple name "Bar") or a member "Type#member". Only
// declared members are resolved; inherited members are out of scope for now.

import type { Fqn, GlobalIndex } from "../compiler/program.ts";
import { getDeclarationNameNode, getSourceFileOfNode } from "../compiler/resolver.ts";
import type { Symbol } from "../compiler/types.ts";
import { isSyntheticUri } from "../workspace.ts";

function resolveType(typeRef: string, index: GlobalIndex): Symbol[] {
  const direct = index.getType(typeRef as Fqn);
  if (direct) return [direct];
  return index
    .findFqnsBySimpleName(typeRef)
    .map(fqn => index.getType(fqn))
    .filter((s): s is Symbol => s !== undefined);
}

export function resolveSymbolRef(ref: string, index: GlobalIndex): Symbol[] {
  const hash = ref.indexOf("#");
  if (hash < 0) return resolveType(ref, index);

  const typeRef = ref.slice(0, hash);
  const memberName = ref.slice(hash + 1);
  const members: Symbol[] = [];
  for (const type of resolveType(typeRef, index)) {
    const member = type.members?.get(memberName);
    if (member) members.push(member);
  }
  return members;
}

// Resolve a ref expected to name exactly one symbol. `ok` tools use `symbol`;
// otherwise the ref was missing (candidates 0) or ambiguous (candidates > 1).
export type SingleRef =
  | { ok: true; symbol: Symbol }
  | { ok: false; ambiguous: boolean; candidates: number };

export function resolveSingleRef(ref: string, index: GlobalIndex): SingleRef {
  const symbols = resolveSymbolRef(ref, index);
  if (symbols.length === 1) return { ok: true, symbol: symbols[0] };
  return { ok: false, ambiguous: symbols.length > 1, candidates: symbols.length };
}

// The MCP convention for an unresolved single-ref result: report the candidate
// count when ambiguous, nothing when simply absent. Spread into the tool's
// otherwise-empty payload.
export function ambiguityFields(r: { ambiguous: boolean; candidates: number }): {
  ambiguous?: true;
  candidates?: number;
} {
  return r.ambiguous ? { ambiguous: true, candidates: r.candidates } : {};
}

// A symbol declared in a synthetic stub (jdk:/// or classpath:///) cannot be
// edited - it has no real file to write back to. Used to refuse rename/edits.
export function isStubSymbol(symbol: Symbol): boolean {
  const declaration = getDeclarationNameNode(symbol);
  return !!declaration && isSyntheticUri(getSourceFileOfNode(declaration).fileName);
}
