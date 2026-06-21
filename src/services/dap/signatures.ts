// JVM type signatures -> human type names, and the JDWP value tag a local's
// signature implies (the tag byte StackFrame.GetValues wants per slot is just
// the first character of the field signature: 'I', 'Z', 'L', '[', ...).
//
// Port reference for togo/internal/dapserver/signatures.go.

const PRIMITIVES: Record<string, string> = {
  B: "byte",
  C: "char",
  D: "double",
  F: "float",
  I: "int",
  J: "long",
  S: "short",
  Z: "boolean",
  V: "void",
};

/** "Ljava/util/List;" -> "java.util.List", "[I" -> "int[]", "I" -> "int". */
export function signatureToType(signature: string): string {
  let depth = 0;
  let i = 0;
  while (signature[i] === "[") {
    depth++;
    i++;
  }
  const base = signature.slice(i);
  let name: string;
  if (base[0] === "L") {
    name = base.slice(1, base.endsWith(";") ? -1 : undefined).replaceAll("/", ".");
  } else {
    name = PRIMITIVES[base[0]] ?? base;
  }
  return name + "[]".repeat(depth);
}

/** The JDWP tag byte for a local with this signature (its first char). */
export function signatureTagByte(signature: string): number {
  return signature.charCodeAt(0);
}
