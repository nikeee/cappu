package dapserver

// JVM type signatures -> human type names, and the JDWP value tag a local's
// signature implies (its first character). Port of
// src/services/dap/signatures.ts.

import "strings"

var primitiveNames = map[byte]string{
	'B': "byte", 'C': "char", 'D': "double", 'F': "float",
	'I': "int", 'J': "long", 'S': "short", 'Z': "boolean", 'V': "void",
}

// SignatureToType renders a JVM field signature: "Ljava/util/List;" ->
// "java.util.List", "[I" -> "int[]", "I" -> "int".
func SignatureToType(signature string) string {
	depth := 0
	for depth < len(signature) && signature[depth] == '[' {
		depth++
	}
	base := signature[depth:]
	var name string
	if len(base) > 0 && base[0] == 'L' {
		inner := strings.TrimSuffix(base[1:], ";")
		name = strings.ReplaceAll(inner, "/", ".")
	} else if len(base) > 0 {
		if p, ok := primitiveNames[base[0]]; ok {
			name = p
		} else {
			name = base
		}
	}
	return name + strings.Repeat("[]", depth)
}

// SignatureTagByte is the JDWP tag byte for a local with this signature.
func SignatureTagByte(signature string) byte {
	if signature == "" {
		return 0
	}
	return signature[0]
}
