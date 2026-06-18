// Turns an agent-supplied symbol reference into engine symbols. References are
// name-addressed (agents do not have file offsets): either a type (FQN
// "com.foo.Bar" or a bare simple name "Bar") or a member "Type#member". Only
// declared members are resolved; inherited members are out of scope for now.

import type { Fqn, GlobalIndex } from "../compiler/program.ts";
import type { Symbol } from "../compiler/types.ts";

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
