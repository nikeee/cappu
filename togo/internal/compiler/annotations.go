package compiler

// Port of the annotation section of src/compiler/bytecode.ts.
//
// Annotation bytecode (JVMS 4.7.16/4.7.17/4.7.18/4.7.19). Annotations are read
// straight off the AST modifiers at each declaration. Each annotation's runtime
// retention decides the attribute it lands in: RUNTIME -> RuntimeVisible*, CLASS
// (the default) -> RuntimeInvisible*, SOURCE -> dropped. Element values are the
// constant-expression arguments, encoded per JVMS 4.7.16.1.

import (
	"strconv"
	"strings"
)

type retention int

const (
	retentionClass retention = iota // the default (RuntimeInvisible)
	retentionRuntime
	retentionSource
)

// Retention of the common JDK annotations, which have no source @interface to
// read. Anything else not declared in source defaults to CLASS (4.7.17).
var jdkRetention = map[internalName]retention{
	"java/lang/Override":              retentionSource,
	"java/lang/SuppressWarnings":      retentionSource,
	"java/lang/Deprecated":            retentionRuntime,
	"java/lang/SafeVarargs":           retentionRuntime,
	"java/lang/FunctionalInterface":   retentionRuntime,
	"java/lang/annotation/Retention":  retentionRuntime,
	"java/lang/annotation/Target":     retentionRuntime,
	"java/lang/annotation/Documented": retentionRuntime,
	"java/lang/annotation/Inherited":  retentionRuntime,
	"java/lang/annotation/Repeatable": retentionRuntime,
	"java/lang/annotation/Native":     retentionSource,
}

// retentionFromMeta reads a @Retention(...) meta-annotation on a source
// @interface, returning the named policy; ("", false) when absent (-> CLASS).
func retentionFromMeta(modifiers *NodeArray) (retention, bool) {
	for _, m := range arrayNodes(modifiers) {
		if m.Kind != Annotation {
			continue
		}
		ann := m.AsAnnotation()
		if lastSegment(entityNameToString(ann.TypeName)) != "Retention" {
			continue
		}
		args := arrayNodes(ann.Args)
		if len(args) == 0 {
			continue
		}
		v := args[0].AsAnnotationArgument().Value
		var name string
		switch v.Kind {
		case PropertyAccessExpression:
			name = v.AsPropertyAccessExpression().Name.AsIdentifier().Text
		case Identifier:
			name = v.AsIdentifier().Text
		}
		switch name {
		case "RUNTIME":
			return retentionRuntime, true
		case "SOURCE":
			return retentionSource, true
		case "CLASS":
			return retentionClass, true
		}
	}
	return retentionClass, false
}

// annotationInfo resolves an annotation's binary name and retention. It reads the
// @interface from source (its @Retention) when available, else the JDK table /
// CLASS default. Returns ("", _) when the type is unresolved (caller skips it).
func annotationInfo(ann *AnnotationData, from *Node, program *Program) (internalName, retention) {
	symbol := ResolveTypeEntityName(ann.TypeName, from, program)
	if symbol == nil {
		return "", retentionClass
	}
	internal := binaryName(symbol)
	for _, d := range symbol.Declarations {
		if d.Kind == AnnotationTypeDeclaration {
			ret, _ := retentionFromMeta(d.AsAnnotationTypeDeclaration().Modifiers)
			return internal, ret
		}
	}
	if r, ok := jdkRetention[internal]; ok {
		return internal, r
	}
	return internal, retentionClass
}

// annotationElementTypes returns the element (method) return-type nodes of a
// source @interface, by element name, so the encoder can pick the exact
// element_value tag. Empty when the type is not a source declaration.
func annotationElementTypes(ann *AnnotationData, from *Node, program *Program) map[string]*Node {
	types := map[string]*Node{}
	symbol := ResolveTypeEntityName(ann.TypeName, from, program)
	if symbol == nil {
		return types
	}
	for _, d := range symbol.Declarations {
		if d.Kind != AnnotationTypeDeclaration {
			continue
		}
		for _, member := range arrayNodes(d.AsAnnotationTypeDeclaration().Members) {
			if member.Kind == MethodDeclaration {
				md := member.AsMethodDeclaration()
				types[md.Name.AsIdentifier().Text] = md.ReturnType
			}
		}
	}
	return types
}

// encodeAnnotation builds one annotation structure (4.7.16): type descriptor +
// name/value pairs. Returns nil if any element value cannot be encoded (the
// caller drops the whole annotation rather than emit malformed bytes).
func encodeAnnotation(cp *constantPool, ann *AnnotationData, from *Node, program *Program) *byteBuffer {
	symbol := ResolveTypeEntityName(ann.TypeName, from, program)
	if symbol == nil {
		return nil
	}
	elementTypes := annotationElementTypes(ann, from, program)
	args := arrayNodes(ann.Args)
	buf := &byteBuffer{}
	buf.u2(int(cp.utf8(string(descOf(binaryName(symbol))))))
	buf.u2(len(args))
	for _, arg := range args {
		a := arg.AsAnnotationArgument()
		name := "value"
		if a.Name != nil {
			name = a.Name.AsIdentifier().Text
		}
		value := encodeElementValue(cp, a.Value, elementTypes[name], from, program)
		if value == nil {
			return nil
		}
		buf.u2(int(cp.utf8(name)))
		buf.appendBuf(value)
	}
	return buf
}

// tagFromDescriptor returns the element_value tag implied by an element's
// declared descriptor; the L...; case ("") is resolved to enum/annotation by the
// value's form.
func tagFromDescriptor(desc descriptor) string {
	s := string(desc)
	if len(s) == 1 && strings.Contains("BSCIJFDZ", s) {
		return s
	}
	if s == "Ljava/lang/String;" {
		return "s"
	}
	if s == "Ljava/lang/Class;" || strings.HasPrefix(s, "Ljava/lang/Class<") {
		return "c"
	}
	if strings.HasPrefix(s, "[") {
		return "["
	}
	return ""
}

// encodeElementValue encodes one element_value (4.7.16.1). Returns nil for an
// unsupported form.
func encodeElementValue(cp *constantPool, raw *Node, elementType *Node, from *Node, program *Program) *byteBuffer {
	value := raw
	for value.Kind == ParenthesizedExpression {
		value = value.AsParenthesizedExpression().Expression
	}
	buf := &byteBuffer{}
	switch value.Kind {
	case ArrayInitializer:
		elems := arrayNodes(value.AsArrayInitializer().Elements)
		buf.u1('[')
		buf.u2(len(elems))
		var elemType *Node
		if elementType != nil && elementType.Kind == ArrayType {
			elemType = elementType.AsArrayType().ElementType
		}
		for _, e := range elems {
			sub := encodeElementValue(cp, e, elemType, from, program)
			if sub == nil {
				return nil
			}
			buf.appendBuf(sub)
		}
		return buf
	case Annotation:
		sub := encodeAnnotation(cp, value.AsAnnotation(), from, program)
		if sub == nil {
			return nil
		}
		buf.u1('@')
		buf.appendBuf(sub)
		return buf
	case ClassLiteralExpression:
		desc := descriptorOf(value.AsClassLiteralExpression().Type, program, nil)
		buf.u1('c')
		buf.u2(int(cp.utf8(string(desc))))
		return buf
	}

	tag := ""
	if elementType != nil {
		tag = tagFromDescriptor(descriptorOf(elementType, program, nil))
	}
	// Enum constant: 'e' type_name const_name (a qualified E.CONST).
	if value.Kind == PropertyAccessExpression && (tag == "e" || tag == "") {
		pa := value.AsPropertyAccessExpression()
		var enumDesc descriptor
		if elementType != nil {
			d := descriptorOf(elementType, program, nil)
			if tagFromDescriptor(d) == "" {
				enumDesc = d
			}
		}
		if enumDesc == "" {
			if sym := ResolveTypeEntityName(pa.Expression, from, program); sym != nil {
				enumDesc = descOf(binaryName(sym))
			}
		}
		if enumDesc == "" {
			return nil
		}
		buf.u1('e')
		buf.u2(int(cp.utf8(string(enumDesc))))
		buf.u2(int(cp.utf8(pa.Name.AsIdentifier().Text)))
		return buf
	}
	return encodeConstElementValue(cp, value, tag, buf)
}

// encodeConstElementValue encodes a primitive / String constant element_value.
// tag is the element's declared tag when known; otherwise inferred from the
// literal.
func encodeConstElementValue(cp *constantPool, value *Node, tag string, buf *byteBuffer) *byteBuffer {
	switch value.Kind {
	case StringLiteral, TextBlockLiteral:
		buf.u1('s')
		buf.u2(int(cp.utf8(value.AsLiteralExpression().Value)))
		return buf
	case TrueKeyword:
		buf.u1('Z')
		buf.u2(int(cp.integer(1)))
		return buf
	case FalseKeyword:
		buf.u1('Z')
		buf.u2(int(cp.integer(0)))
		return buf
	case CharacterLiteral:
		v := value.AsLiteralExpression().Value
		if v == "" {
			return nil
		}
		buf.u1('C')
		buf.u2(int(cp.integer(int(v[0]))))
		return buf
	}
	neg := false
	lit := value
	if lit.Kind == PrefixUnaryExpression && lit.AsPrefixUnaryExpression().Operator == MinusToken {
		neg = true
		lit = lit.AsPrefixUnaryExpression().Operand
	}
	if lit.Kind != NumericLiteral {
		return nil
	}
	text := strings.ReplaceAll(lit.AsLiteralExpression().Value, "_", "")
	isHex := strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X")
	isBin := strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B")
	inferred := "I"
	switch {
	case !isHex && !isBin && strings.HasSuffix(strings.ToLower(text), "f"):
		inferred = "F"
	case !isHex && !isBin && (strings.ContainsAny(text, ".eE") || strings.HasSuffix(strings.ToLower(text), "d")):
		inferred = "D"
	case strings.HasSuffix(strings.ToLower(text), "l"):
		inferred = "J"
	}
	t := inferred
	if len(tag) == 1 && strings.Contains("BSCIJFDZ", tag) {
		t = tag
	}
	sign := 1.0
	if neg {
		sign = -1.0
	}
	switch t {
	case "F":
		f, err := strconv.ParseFloat(strings.TrimRight(text, "fF"), 32)
		if err != nil {
			return nil
		}
		buf.u1('F')
		buf.u2(int(cp.float(sign * f)))
		return buf
	case "D":
		d, err := strconv.ParseFloat(strings.TrimRight(text, "dD"), 64)
		if err != nil {
			return nil
		}
		buf.u1('D')
		buf.u2(int(cp.double(sign * d)))
		return buf
	case "J":
		u, ok := parseIntLiteral(strings.TrimRight(text, "lL"))
		if !ok {
			return nil
		}
		v := int64(u)
		if neg {
			v = -v
		}
		buf.u1('J')
		buf.u2(int(cp.long(v)))
		return buf
	default: // B, S, C, Z, I all use a CONSTANT_Integer.
		u, ok := parseIntLiteral(text)
		if !ok {
			return nil
		}
		v := int32(u)
		if neg {
			v = -v
		}
		buf.u1(int(t[0]))
		buf.u2(int(cp.integer(int(v))))
		return buf
	}
}

// annotationsAttributeBody builds a Runtime{Visible,Invisible}Annotations body
// for the annotations in `modifiers` that fall in the given retention bucket, or
// nil when none. `visible` selects RUNTIME vs CLASS-default.
func annotationsAttributeBody(cp *constantPool, modifiers *NodeArray, visible bool, from *Node, program *Program) *byteBuffer {
	var encoded []*byteBuffer
	for _, m := range arrayNodes(modifiers) {
		if m.Kind != Annotation {
			continue
		}
		_, ret := annotationInfo(m.AsAnnotation(), from, program)
		if ResolveTypeEntityName(m.AsAnnotation().TypeName, from, program) == nil {
			continue
		}
		if ret == retentionSource {
			continue
		}
		if (ret == retentionRuntime) != visible {
			continue
		}
		if enc := encodeAnnotation(cp, m.AsAnnotation(), from, program); enc != nil {
			encoded = append(encoded, enc)
		}
	}
	if len(encoded) == 0 {
		return nil
	}
	body := &byteBuffer{}
	body.u2(len(encoded))
	for _, e := range encoded {
		body.appendBuf(e)
	}
	return body
}

// writeAnnotationAttributes appends the Runtime{Visible,Invisible}Annotations
// attributes for `modifiers`, returning how many attributes were written.
func writeAnnotationAttributes(buffer *byteBuffer, cp *constantPool, modifiers *NodeArray, from *Node, program *Program) int {
	count := 0
	for _, visible := range []bool{true, false} {
		body := annotationsAttributeBody(cp, modifiers, visible, from, program)
		if body == nil {
			continue
		}
		name := "RuntimeInvisibleAnnotations"
		if visible {
			name = "RuntimeVisibleAnnotations"
		}
		buffer.u2(int(cp.utf8(name)))
		buffer.u4(body.length())
		buffer.appendBuf(body)
		count++
	}
	return count
}

// parameterAnnotationsBody builds a Runtime{Visible,Invisible}ParameterAnnotations
// body (4.7.18/4.7.19), or nil when no parameter is annotated.
func parameterAnnotationsBody(cp *constantPool, parameters *NodeArray, visible bool, from *Node, program *Program) *byteBuffer {
	var formal []*Node
	for _, p := range arrayNodes(parameters) {
		if !p.AsParameter().IsReceiver {
			formal = append(formal, p)
		}
	}
	total := 0
	perParam := make([]*byteBuffer, len(formal))
	for i, p := range formal {
		var encoded []*byteBuffer
		for _, m := range arrayNodes(p.AsParameter().Modifiers) {
			if m.Kind != Annotation {
				continue
			}
			_, ret := annotationInfo(m.AsAnnotation(), from, program)
			if ResolveTypeEntityName(m.AsAnnotation().TypeName, from, program) == nil {
				continue
			}
			if ret == retentionSource {
				continue
			}
			if (ret == retentionRuntime) != visible {
				continue
			}
			if enc := encodeAnnotation(cp, m.AsAnnotation(), from, program); enc != nil {
				encoded = append(encoded, enc)
			}
		}
		total += len(encoded)
		b := &byteBuffer{}
		b.u2(len(encoded))
		for _, e := range encoded {
			b.appendBuf(e)
		}
		perParam[i] = b
	}
	if total == 0 {
		return nil
	}
	body := &byteBuffer{}
	body.u1(len(formal)) // num_parameters
	for _, b := range perParam {
		body.appendBuf(b)
	}
	return body
}

// methodAnnotationAttributes returns the method-level annotation attributes
// (Runtime{Visible,Invisible}Annotations then the parameter variants), in javac's
// order, as a buffer + attribute count.
func methodAnnotationAttributes(cp *constantPool, method *MethodDeclarationData, from *Node, program *Program) (*byteBuffer, int) {
	buffer := &byteBuffer{}
	count := writeAnnotationAttributes(buffer, cp, method.Modifiers, from, program)
	for _, visible := range []bool{true, false} {
		body := parameterAnnotationsBody(cp, method.Parameters, visible, from, program)
		if body == nil {
			continue
		}
		name := "RuntimeInvisibleParameterAnnotations"
		if visible {
			name = "RuntimeVisibleParameterAnnotations"
		}
		buffer.u2(int(cp.utf8(name)))
		buffer.u4(body.length())
		buffer.appendBuf(body)
		count++
	}
	return buffer, count
}
