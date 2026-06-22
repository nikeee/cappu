package compiler

import (
	"fmt"
	"strings"
)

// symbolDecl returns the symbol's value declaration, or its first declaration.
func symbolDecl(s *Symbol) *Node {
	if s == nil {
		return nil
	}
	if s.ValueDeclaration != nil {
		return s.ValueDeclaration
	}
	if len(s.Declarations) > 0 {
		return s.Declarations[0]
	}
	return nil
}

// declModifiers returns the modifiers NodeArray for any declaration kind that has them.
func declModifiers(node *Node) *NodeArray {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case ClassDeclaration:
		return node.AsClassDeclaration().Modifiers
	case InterfaceDeclaration:
		return node.AsInterfaceDeclaration().Modifiers
	case EnumDeclaration:
		return node.AsEnumDeclaration().Modifiers
	case RecordDeclaration:
		return node.AsRecordDeclaration().Modifiers
	case MethodDeclaration:
		return node.AsMethodDeclaration().Modifiers
	case ConstructorDeclaration:
		return node.AsConstructorDeclaration().Modifiers
	case FieldDeclaration:
		return node.AsFieldDeclaration().Modifiers
	case Parameter:
		return node.AsParameter().Modifiers
	default:
		return nil
	}
}

// hasModifierKind reports whether the modifier list contains a modifier of kind.
func hasModifierKind(mods *NodeArray, kind SyntaxKind) bool {
	if mods == nil {
		return false
	}
	for _, m := range mods.Nodes {
		if m.Kind == kind {
			return true
		}
	}
	return false
}

func arrayNodes(a *NodeArray) []*Node {
	if a == nil {
		return nil
	}
	return a.Nodes
}

// membersOf returns the member list of any type-declaration node.
func membersOf(node *Node) []*Node {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case ClassDeclaration:
		return arrayNodes(node.AsClassDeclaration().Members)
	case InterfaceDeclaration:
		return arrayNodes(node.AsInterfaceDeclaration().Members)
	case EnumDeclaration:
		return arrayNodes(node.AsEnumDeclaration().Members)
	case RecordDeclaration:
		return arrayNodes(node.AsRecordDeclaration().Members)
	case AnnotationTypeDeclaration:
		return arrayNodes(node.AsAnnotationTypeDeclaration().Members)
	default:
		return nil
	}
}

// --- access flags -----------------------------------------------------------

func classAccessFlags(declaration *Node) int {
	flags := accSuper
	for _, modifier := range arrayNodes(declaration.AsClassDeclaration().Modifiers) {
		switch modifier.Kind {
		case PublicKeyword:
			flags |= accPublic
		case FinalKeyword:
			flags |= accFinal
		case AbstractKeyword:
			flags |= accAbstract
		}
	}
	return flags
}

func memberAccessFlags(modifiers *NodeArray) int {
	flags := 0
	for _, modifier := range arrayNodes(modifiers) {
		switch modifier.Kind {
		case PublicKeyword:
			flags |= accPublic
		case PrivateKeyword:
			flags |= accPrivate
		case ProtectedKeyword:
			flags |= accProtected
		case StaticKeyword:
			flags |= accStatic
		case FinalKeyword:
			flags |= accFinal
		case VolatileKeyword:
			flags |= accVolatile
		case TransientKeyword:
			flags |= accTransient
		}
	}
	return flags
}

// sourceNameOf returns the source file's base name (for the SourceFile attribute), or "".
func sourceNameOf(node *Node) string {
	n := node
	for n != nil && n.Kind != SourceFile {
		n = n.Parent
	}
	if n == nil {
		return ""
	}
	fileName := n.AsSourceFile().FileName
	if fileName == "" {
		return ""
	}
	parts := strings.Split(fileName, "/")
	return parts[len(parts)-1]
}

// --- inner-class records ----------------------------------------------------

// innerClassRecord is one inner_classes entry (JVMS 4.7.6) for a nested class.
type innerClassRecord struct {
	outer      internalName // immediately enclosing class; "" for local/anonymous
	simpleName string       // source simple name; "" for anonymous
	flags      int          // inner_class_access_flags: source-level modifiers
}

const lookupName internalName = "java/lang/invoke/MethodHandles$Lookup"

// innerClassFlags returns the InnerClasses access flags of a nested declaration.
func innerClassFlags(node *Node) int {
	flags := memberAccessFlags(declModifiers(node))
	// A type declared inside an interface is implicitly public and static (JLS 9.5).
	if node.Parent != nil && node.Parent.Kind == InterfaceDeclaration {
		flags |= accPublic | accStatic
	}
	switch node.Kind {
	case InterfaceDeclaration:
		flags |= accInterface | accAbstract | accStatic
	case EnumDeclaration:
		flags |= accEnum | accStatic | accFinal
	case RecordDeclaration:
		flags |= accFinal | accStatic
	}
	return flags
}

// --- type descriptors -------------------------------------------------------

// typeToDescriptor is the field/parameter descriptor of a checker Type (erasing
// type variables and wildcards to Object). Module-level twin of generateBody's
// typeDescriptor, for capture analysis which runs outside that closure.
func typeToDescriptor(t *Type, depth int) descriptor {
	switch t.Kind {
	case TypeKindPrimitive:
		if d, ok := primitiveDescriptor(t.Name); ok {
			return d
		}
		return "I"
	case TypeKindClass:
		return descriptor("L" + string(binaryName(t.Symbol)) + ";")
	case TypeKindArray:
		return descriptor("[" + string(typeToDescriptor(t.ElementType, depth)))
	case TypeKindTypeVariable:
		// Erasure to the leftmost bound (JLS 4.6); the depth guard caps a
		// (malformed) `T extends U, U extends T` chain.
		if t.Bound != nil && depth < 8 {
			return typeToDescriptor(t.Bound, depth+1)
		}
		return objectDesc
	default:
		return objectDesc
	}
}

// binaryName is the internal (binary) name of a type symbol: package with '/'
// separators, nested types joined by '$'. e.g. java.lang.String -> "java/lang/String".
func binaryName(symbol *Symbol) internalName {
	names := []string{symbol.EscapedName}
	parent := symbol.Parent
	for parent != nil && parent.Flags&SymbolFlagsType != 0 {
		names = append([]string{parent.EscapedName}, names...)
		parent = parent.Parent
	}
	pkg := ""
	if parent != nil && parent.Flags&SymbolFlagsPackage != 0 {
		pkg = parent.EscapedName
	}
	// A local class's symbol-parent chain stops at the enclosing method/block (not
	// a type), so no type prefix was collected. Recover the enclosing type from
	// the AST and number the class as javac does: Outer$<k><Name>.
	if pkg == "" && len(names) == 1 {
		decl := symbolDecl(symbol)
		var node *Node
		if decl != nil {
			node = decl.Parent
		}
		for node != nil && !isTypeDeclarationKind(node.Kind) {
			node = node.Parent
		}
		if node != nil && node.Symbol != nil && decl != nil {
			index := 1
			var count func(n *Node)
			count = func(n *Node) {
				if n.Kind == ClassDeclaration && n != decl &&
					n.Parent != nil && n.Parent.Kind == Block &&
					n.AsClassDeclaration().Name != nil &&
					n.AsClassDeclaration().Name.AsIdentifier().Text == symbol.EscapedName &&
					n.Pos < decl.Pos {
					index++
				}
				if n != node && isTypeDeclarationKind(n.Kind) {
					return
				}
				n.ForEachChild(func(c *Node) bool {
					count(c)
					return false
				})
			}
			count(node)
			return internalName(fmt.Sprintf("%s$%d%s", binaryName(node.Symbol), index, symbol.EscapedName))
		}
	}
	nested := strings.Join(names, "$")
	if pkg != "" {
		return internalName(strings.ReplaceAll(pkg, ".", "/") + "/" + nested)
	}
	return internalName(nested)
}

// descriptorOf is the field/return type descriptor (JVMS 4.3.2) of a written
// type. Type references are resolved to a binary name; an unresolved name falls
// back to its written form (best effort).
func descriptorOf(typeNode *Node, program *Program, seenParams map[*Symbol]bool) descriptor {
	switch typeNode.Kind {
	case PrimitiveType:
		keyword := tokenToString(typeNode.AsPrimitiveType().Keyword)
		if keyword == "" {
			keyword = "int"
		}
		if d, ok := primitiveDescriptor(keyword); ok {
			return d
		}
		return "I"
	case ArrayType:
		return descriptor("[" + string(descriptorOf(typeNode.AsArrayType().ElementType, program, seenParams)))
	case TypeReference:
		ref := typeNode.AsTypeReference()
		symbol := ResolveTypeEntityName(ref.TypeName, typeNode, program)
		// A type variable erases to its leftmost bound, or Object if unbounded (JLS 4.6).
		if symbol != nil && symbol.Flags&SymbolFlagsTypeParameter != 0 {
			if seenParams != nil && seenParams[symbol] {
				return objectDesc
			}
			declaration := symbolDecl(symbol)
			var constraint *Node
			if declaration != nil && declaration.Kind == TypeParameter {
				if bounds := arrayNodes(declaration.AsTypeParameter().Constraint); len(bounds) > 0 {
					constraint = bounds[0]
				}
			}
			if constraint == nil {
				return objectDesc
			}
			if seenParams == nil {
				seenParams = map[*Symbol]bool{}
			}
			seenParams[symbol] = true
			return descriptorOf(constraint, program, seenParams)
		}
		var name internalName
		if symbol != nil {
			name = binaryName(symbol)
		} else {
			name = internalName(strings.ReplaceAll(entityNameToString(ref.TypeName), ".", "/"))
		}
		return descOf(name)
	default:
		return objectDesc
	}
}

// --- captures ---------------------------------------------------------------

// localCapture is a local variable / parameter of an enclosing method captured
// by a local class (JLS 14.3 / 8.1.3): stored in a synthetic final field val$<name>.
type localCapture struct {
	symbol     *Symbol
	fieldName  string
	descriptor descriptor
}

func computeLocalCaptures(decl *Node, program *Program, checker *Checker) []localCapture {
	d := decl.AsClassDeclaration()
	return collectCaptures(arrayNodes(d.Members), decl.Pos, decl.End, program, checker)
}

// collectCaptures returns the enclosing locals/parameters referenced inside a
// class body spanning [lo, hi), in first-use order.
func collectCaptures(members []*Node, lo, hi int, program *Program, checker *Checker) []localCapture {
	var result []localCapture
	seen := map[*Symbol]bool{}
	within := func(n *Node) bool { return n != nil && n.Pos >= lo && n.End <= hi }
	var visit func(node *Node)
	visit = func(node *Node) {
		if node.Kind == Identifier {
			parent := node.Parent
			isMemberName := parent != nil && parent.Kind == PropertyAccessExpression &&
				parent.AsPropertyAccessExpression().Name == node
			if !isMemberName {
				sym := ResolveIdentifier(node, program)
				if sym != nil && sym.Flags&(SymbolFlagsLocalVariable|SymbolFlagsParameter) != 0 && !seen[sym] {
					declNode := symbolDecl(sym)
					if declNode != nil && !within(declNode) {
						seen[sym] = true
						result = append(result, localCapture{
							symbol:     sym,
							fieldName:  "val$" + sym.EscapedName,
							descriptor: typeToDescriptor(checker.GetTypeOfSymbol(sym), 0),
						})
					}
				}
			}
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	for _, member := range members {
		visit(member)
	}
	return result
}

// outerThisInfo returns the enclosing type's internal name when a class body
// accesses the enclosing instance from a non-static context (so it must capture
// this$0); else "".
func outerThisInfo(members []*Node, parent *Node, program *Program, checker *Checker) internalName {
	var typeSym *Symbol
	for n := parent; n != nil; n = n.Parent {
		if n.Kind == MethodDeclaration && hasModifierKind(n.AsMethodDeclaration().Modifiers, StaticKeyword) {
			return ""
		}
		if isTypeDeclarationKind(n.Kind) {
			typeSym = n.Symbol
			break
		}
	}
	if typeSym == nil {
		return ""
	}
	used := false
	var visit func(node *Node)
	visit = func(node *Node) {
		if used {
			return
		}
		if node.Kind == ThisExpression {
			q := node.AsThisExpression().Qualifier
			if q != nil {
				qt := checker.GetTypeOfExpression(q)
				if qt.Kind == TypeKindClass && qt.Symbol == typeSym {
					used = true
				}
			}
		}
		if node.Kind == Identifier {
			p := node.Parent
			isMemberName := p != nil && p.Kind == PropertyAccessExpression && p.AsPropertyAccessExpression().Name == node
			isCallee := p != nil && p.Kind == CallExpression && p.AsCallExpression().Expression == node
			if !isMemberName && !isCallee {
				s := ResolveIdentifier(node, program)
				var fieldDecl *Node
				if s != nil {
					if vd := symbolDecl(s); vd != nil {
						fieldDecl = vd.Parent
					}
				}
				if s != nil && s.Flags&SymbolFlagsField != 0 && s.Flags&SymbolFlagsEnumConstant == 0 &&
					s.Parent == typeSym && fieldDecl != nil && fieldDecl.Kind == FieldDeclaration &&
					!isStaticDeclaration(fieldDecl) {
					used = true
				}
			}
		} else if node.Kind == CallExpression && node.AsCallExpression().Expression.Kind == Identifier {
			m := checker.ResolveCall(node)
			if m != nil && m.Symbol != nil && m.Symbol.Parent == typeSym && !isStaticDeclaration(m) {
				used = true
			}
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	for _, member := range members {
		visit(member)
	}
	if used {
		return binaryName(typeSym)
	}
	return ""
}

// isSynthesizableLocalClass reports a local class (declared in a block) whose
// constructor handling we support emitting.
func isSynthesizableLocalClass(decl *Node) bool {
	return decl.Parent != nil && decl.Parent.Kind == Block
}

func effectiveLocalCaptures(decl *Node, program *Program, checker *Checker) []localCapture {
	if isSynthesizableLocalClass(decl) {
		return computeLocalCaptures(decl, program, checker)
	}
	return nil
}

// localOuterThis is the enclosing instance a synthesizable local class captures (this$0), or "".
func localOuterThis(decl *Node, program *Program, checker *Checker) internalName {
	if isSynthesizableLocalClass(decl) {
		return outerThisInfo(arrayNodes(decl.AsClassDeclaration().Members), decl.Parent, program, checker)
	}
	return ""
}

// memberInnerThis0 is the enclosing instance (this$0) a non-static member inner class captures, or "".
func memberInnerThis0(decl *Node, program *Program, checker *Checker) internalName {
	if decl.Parent == nil || !isTypeDeclarationKind(decl.Parent.Kind) {
		return ""
	}
	if isStaticDeclaration(decl) {
		return ""
	}
	return outerThisInfo(arrayNodes(decl.AsClassDeclaration().Members), decl.Parent, program, checker)
}

// --- field emission + constants ---------------------------------------------

// fieldInit is a field initializer (owner/name/descriptor/init) or an
// initializer block (JLS 8.6 / 8.7) that runs its statements in place.
type fieldInit struct {
	isStatic   bool
	owner      internalName
	name       string
	descriptor descriptor
	init       *Node
	block      *Node // when set, an initializer block; init/name/owner unused
}

// collectFieldInits splits field initializers and initializer blocks by static-ness.
func collectFieldInits(members []*Node, ownerName internalName, program *Program) (instanceInits, staticInits []fieldInit) {
	for _, member := range members {
		if member.Kind == InitializerBlock {
			blk := member.AsInitializerBlock()
			fi := fieldInit{isStatic: blk.IsStatic, block: blk.Body}
			if blk.IsStatic {
				staticInits = append(staticInits, fi)
			} else {
				instanceInits = append(instanceInits, fi)
			}
			continue
		}
		if member.Kind != FieldDeclaration {
			continue
		}
		field := member.AsFieldDeclaration()
		isStatic := isStaticDeclaration(member)
		baseDescriptor := descriptorOf(field.Type, program, nil)
		for _, declarator := range arrayNodes(field.Declarators) {
			d := declarator.AsVariableDeclarator()
			desc := withRank(baseDescriptor, d.ArrayRankAfterName)
			if d.Initializer == nil {
				continue
			}
			if isStatic && isConstantValueField(member, declarator, program) {
				continue
			}
			fi := fieldInit{isStatic: isStatic, owner: ownerName, name: d.Name.AsIdentifier().Text, descriptor: desc, init: d.Initializer}
			if isStatic {
				staticInits = append(staticInits, fi)
			} else {
				instanceInits = append(instanceInits, fi)
			}
		}
	}
	return instanceInits, staticInits
}

// emitFields writes one field_info per declarator (int a, b; emits two fields).
func emitFields(declaration *Node, cp *constantPool, program *Program) (*byteBuffer, int) {
	return emitFieldsFromMembers(membersOf(declaration), cp, program)
}

func emitFieldsFromMembers(members []*Node, cp *constantPool, program *Program) (*byteBuffer, int) {
	buffer := &byteBuffer{}
	count := 0
	for _, member := range members {
		if member.Kind != FieldDeclaration {
			continue
		}
		field := member.AsFieldDeclaration()
		baseDescriptor := descriptorOf(field.Type, program, nil)
		flags := memberAccessFlags(field.Modifiers)
		signature := jvmSignature("")
		hasSignature := false
		if typeUsesGenerics(field.Type, program) {
			signature = signatureOfType(field.Type, program)
			hasSignature = true
		}
		for _, declarator := range arrayNodes(field.Declarators) {
			d := declarator.AsVariableDeclarator()
			desc := withRank(baseDescriptor, d.ArrayRankAfterName)
			buffer.u2(flags)
			buffer.u2(int(cp.utf8(d.Name.AsIdentifier().Text)))
			buffer.u2(int(cp.utf8(string(desc))))
			constIndex, hasConst := constantValueIndex(member, declarator, cp, program)
			nAttr := 0
			if hasConst {
				nAttr++
			}
			if hasSignature {
				nAttr++
			}
			buffer.u2(nAttr)
			if hasConst {
				buffer.u2(int(cp.utf8("ConstantValue")))
				buffer.u4(2)
				buffer.u2(int(constIndex))
			}
			if hasSignature {
				writeSignatureAttribute(buffer, cp, signature)
			}
			count++
		}
	}
	return buffer, count
}

func hasFinalModifier(modifiers *NodeArray) bool {
	return hasModifierKind(modifiers, FinalKeyword)
}

// isConstantValueField reports a `static final` field whose initializer is a
// constant eligible for a ConstantValue attribute (so it is excluded from <clinit>).
func isConstantValueField(field, declarator *Node, program *Program) bool {
	fd := field.AsFieldDeclaration()
	d := declarator.AsVariableDeclarator()
	if !isStaticDeclaration(field) || !hasFinalModifier(fd.Modifiers) || d.Initializer == nil {
		return false
	}
	desc := descriptorOf(fd.Type, program, nil)
	if desc == stringDesc && d.Initializer.Kind == StringLiteral {
		return true
	}
	if FoldConstant(d.Initializer) == nil {
		return false
	}
	switch desc {
	case "J", "Z", "I", "S", "B", "C":
		return true
	default:
		return false
	}
}

// constantValueIndex is the constant-pool index of a field's ConstantValue (JVMS
// 4.7.2); ok=false when the field has none.
func constantValueIndex(field, declarator *Node, cp *constantPool, program *Program) (cpIndex, bool) {
	if !isConstantValueField(field, declarator, program) {
		return 0, false
	}
	fd := field.AsFieldDeclaration()
	d := declarator.AsVariableDeclarator()
	init := d.Initializer
	desc := descriptorOf(fd.Type, program, nil)
	if desc == stringDesc {
		return cp.stringConst(init.AsLiteralExpression().Value), true
	}
	folded := FoldConstant(init)
	var intValue int64
	if folded.Kind == ConstBool {
		if folded.Bool {
			intValue = 1
		}
	} else {
		intValue = folded.Int
	}
	if desc == "J" {
		return cp.long(intValue), true
	}
	return cp.integer(int(int32(intValue))), true
}

// --- method descriptors + signatures ----------------------------------------

func methodAccessFlags(method *Node) int {
	flags := 0
	for _, modifier := range arrayNodes(declModifiers(method)) {
		switch modifier.Kind {
		case PublicKeyword:
			flags |= accPublic
		case PrivateKeyword:
			flags |= accPrivate
		case ProtectedKeyword:
			flags |= accProtected
		case StaticKeyword:
			flags |= accStatic
		case FinalKeyword:
			flags |= accFinal
		case AbstractKeyword:
			flags |= accAbstract
		case SynchronizedKeyword:
			flags |= accSynchronized
		case NativeKeyword:
			flags |= accNative
		case StrictfpKeyword:
			flags |= accStrict
		}
	}
	for _, p := range methodParameters(method) {
		if p.AsParameter().IsVarArgs {
			flags |= accVarargs
			break
		}
	}
	return flags
}

// methodParameters returns the parameter nodes of a method or constructor.
func methodParameters(method *Node) []*Node {
	switch method.Kind {
	case MethodDeclaration:
		return arrayNodes(method.AsMethodDeclaration().Parameters)
	case ConstructorDeclaration:
		return arrayNodes(method.AsConstructorDeclaration().Parameters)
	default:
		return nil
	}
}

func paramDescriptor(parameter *Node, program *Program) descriptor {
	p := parameter.AsParameter()
	base := withRank(descriptorOf(p.Type, program, nil), p.ArrayRankAfterName)
	if p.IsVarArgs {
		return descriptor("[" + string(base)) // T... is T[] at the bytecode level
	}
	return base
}

func methodDescriptorOf(method *Node, program *Program) methodDescriptor {
	params := ""
	for _, p := range arrayNodes(method.AsMethodDeclaration().Parameters) {
		params += string(paramDescriptor(p, program))
	}
	return methodDescriptor("(" + params + ")" + string(descriptorOf(method.AsMethodDeclaration().ReturnType, program, nil)))
}

// typeUsesGenerics reports whether the written type mentions generics (a type
// variable or type arguments), i.e. whether its signature would differ from its
// erased descriptor.
func typeUsesGenerics(typeNode *Node, program *Program) bool {
	if typeNode == nil {
		return false
	}
	switch typeNode.Kind {
	case ArrayType:
		return typeUsesGenerics(typeNode.AsArrayType().ElementType, program)
	case TypeReference:
		ref := typeNode.AsTypeReference()
		if len(arrayNodes(ref.TypeArguments)) > 0 {
			return true
		}
		symbol := ResolveTypeEntityName(ref.TypeName, typeNode, program)
		return symbol != nil && symbol.Flags&SymbolFlagsTypeParameter != 0
	default:
		return false
	}
}

// signatureOfType is the JavaTypeSignature for a written type: like descriptorOf,
// but a type variable stays `TName;` and type arguments are kept.
func signatureOfType(typeNode *Node, program *Program) jvmSignature {
	switch typeNode.Kind {
	case PrimitiveType:
		keyword := tokenToString(typeNode.AsPrimitiveType().Keyword)
		if keyword == "" {
			keyword = "int"
		}
		if d, ok := primitiveDescriptor(keyword); ok {
			return jvmSignature(d)
		}
		return "I"
	case ArrayType:
		return jvmSignature("[" + string(signatureOfType(typeNode.AsArrayType().ElementType, program)))
	case TypeReference:
		ref := typeNode.AsTypeReference()
		symbol := ResolveTypeEntityName(ref.TypeName, typeNode, program)
		if symbol != nil && symbol.Flags&SymbolFlagsTypeParameter != 0 {
			return jvmSignature("T" + symbol.EscapedName + ";")
		}
		var name internalName
		if symbol != nil {
			name = binaryName(symbol)
		} else {
			name = internalName(strings.ReplaceAll(entityNameToString(ref.TypeName), ".", "/"))
		}
		args := arrayNodes(ref.TypeArguments)
		if len(args) == 0 {
			return jvmSignature(descOf(name))
		}
		s := ""
		for _, a := range args {
			s += signatureOfTypeArgument(a, program)
		}
		return jvmSignature("L" + string(name) + "<" + s + ">;")
	default:
		return jvmSignature(objectDesc)
	}
}

func signatureOfTypeArgument(node *Node, program *Program) string {
	if node.Kind == WildcardType {
		w := node.AsWildcardType()
		if w.HasExtends && w.Type != nil {
			return "+" + string(signatureOfType(w.Type, program))
		}
		if w.HasSuper && w.Type != nil {
			return "-" + string(signatureOfType(w.Type, program))
		}
		return "*"
	}
	return string(signatureOfType(node, program))
}

// typeParamsSignature renders FormalTypeParameters (JVMS 4.7.9.1).
func typeParamsSignature(typeParameters []*Node, program *Program) string {
	if len(typeParameters) == 0 {
		return ""
	}
	out := "<"
	for _, tp := range typeParameters {
		tpd := tp.AsTypeParameter()
		out += tpd.Name.AsIdentifier().Text
		bounds := arrayNodes(tpd.Constraint)
		if len(bounds) == 0 {
			out += ":Ljava/lang/Object;"
			continue
		}
		for i, bound := range bounds {
			var symbol *Symbol
			if bound.Kind == TypeReference {
				symbol = ResolveTypeEntityName(bound.AsTypeReference().TypeName, bound, program)
			}
			isInterface := symbol != nil && symbol.Flags&SymbolFlagsInterface != 0
			if i == 0 && isInterface {
				out += "::"
			} else {
				out += ":"
			}
			out += string(signatureOfType(bound, program))
		}
	}
	return out + ">"
}

// methodSignatureOf is the MethodSignature, or ("", false) when the erased
// descriptor already says it all.
func methodSignatureOf(method *Node, program *Program) (jvmSignature, bool) {
	var returnType *Node
	var typeParameters []*Node
	if method.Kind == MethodDeclaration {
		returnType = method.AsMethodDeclaration().ReturnType
		typeParameters = arrayNodes(method.AsMethodDeclaration().TypeParameters)
	} else {
		typeParameters = arrayNodes(method.AsConstructorDeclaration().TypeParameters)
	}
	params := methodParameters(method)
	generic := len(typeParameters) > 0 || typeUsesGenerics(returnType, program)
	for _, p := range params {
		if typeUsesGenerics(p.AsParameter().Type, program) {
			generic = true
		}
	}
	if !generic {
		return "", false
	}
	ps := ""
	for _, p := range params {
		param := p.AsParameter()
		s := signatureOfType(param.Type, program)
		if param.IsVarArgs {
			ps += "[" + string(s)
		} else {
			ps += string(s)
		}
	}
	ret := "V"
	if returnType != nil {
		ret = string(signatureOfType(returnType, program))
	}
	return jvmSignature(typeParamsSignature(typeParameters, program) + "(" + ps + ")" + ret), true
}

// classSignatureOf is the ClassSignature, or ("", false) for a non-generic declaration.
func classSignatureOf(declaration *Node, program *Program) (jvmSignature, bool) {
	var extendsType *Node
	var typeParameters []*Node
	var supers []*Node
	switch declaration.Kind {
	case ClassDeclaration:
		d := declaration.AsClassDeclaration()
		extendsType = d.ExtendsType
		typeParameters = arrayNodes(d.TypeParameters)
		supers = append(supers, arrayNodes(d.ImplementsTypes)...)
	case InterfaceDeclaration:
		d := declaration.AsInterfaceDeclaration()
		typeParameters = arrayNodes(d.TypeParameters)
		supers = append(supers, arrayNodes(d.ExtendsTypes)...)
	case EnumDeclaration:
		d := declaration.AsEnumDeclaration()
		supers = append(supers, arrayNodes(d.ImplementsTypes)...)
	}
	generic := len(typeParameters) > 0 || typeUsesGenerics(extendsType, program)
	for _, t := range supers {
		if typeUsesGenerics(t, program) {
			generic = true
		}
	}
	if !generic {
		return "", false
	}
	sup := string(objectDesc)
	if extendsType != nil {
		sup = string(signatureOfType(extendsType, program))
	}
	ifaces := ""
	for _, t := range supers {
		ifaces += string(signatureOfType(t, program))
	}
	return jvmSignature(typeParamsSignature(typeParameters, program) + sup + ifaces), true
}

// --- descriptor utilities ---------------------------------------------------

// slotsOf returns one slot per value, two for long/double (JVMS 2.6.1).
func slotsOf(d descriptor) int {
	if d == "J" || d == "D" {
		return 2
	}
	return 1
}

// defaultReturnBody is a placeholder body: return the default value for the return type.
func defaultReturnBody(returnDescriptor descriptor) (*byteBuffer, int) {
	code := &byteBuffer{}
	switch returnDescriptor[0] {
	case 'V':
		code.u1(opReturn)
		return code, 0
	case 'J':
		code.u1(opLconst0)
		code.u1(opLreturn)
		return code, 2
	case 'D':
		code.u1(opDconst0)
		code.u1(opDreturn)
		return code, 2
	case 'F':
		code.u1(opFconst0)
		code.u1(opFreturn)
		return code, 1
	case 'L', '[':
		code.u1(opAconstNull)
		code.u1(opAreturn)
		return code, 1
	default: // B C S Z I
		code.u1(opIconst0)
		code.u1(opIreturn)
		return code, 1
	}
}

// returnDescriptorOf is what follows the `)` of a method descriptor.
func returnDescriptorOf(d methodDescriptor) descriptor {
	s := string(d)
	return descriptor(s[strings.LastIndexByte(s, ')')+1:])
}

func parseParamDescriptors(md methodDescriptor) []descriptor {
	var params []descriptor
	s := string(md)
	i := strings.IndexByte(s, '(') + 1
	for s[i] != ')' {
		start := i
		for s[i] == '[' {
			i++
		}
		if s[i] == 'L' {
			i = strings.IndexByte(s[i:], ';') + i + 1
		} else {
			i++
		}
		params = append(params, descriptor(s[start:i]))
	}
	return params
}

func isStaticDeclaration(declaration *Node) bool {
	return hasModifierKind(declModifiers(declaration), StaticKeyword)
}

// widensTo holds primitive widening targets (JLS 5.1.2), for constructor-argument matching.
var widensTo = map[descriptor]string{
	"B": "SIJFD", "S": "IJFD", "C": "IJFD", "I": "JFD", "J": "FD", "F": "D",
}

type findCtorRefs struct {
	checker  *Checker
	argTypes []*Type
}

// findConstructor returns the constructor of typeSymbol applicable to the
// arguments (see the JLS-5.3 disambiguation in the TS source), or nil.
func findConstructor(typeSymbol *Symbol, argCount int, program *Program, argDescs []descriptor, refs *findCtorRefs) *Node {
	declaration := symbolDecl(typeSymbol)
	var candidates []*Node
	for _, m := range membersOf(declaration) {
		if m.Kind == ConstructorDeclaration && len(arrayNodes(m.AsConstructorDeclaration().Parameters)) == argCount {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) <= 1 || argDescs == nil || program == nil {
		if len(candidates) > 0 {
			return candidates[0]
		}
		return nil
	}
	paramsOf := func(c *Node) []descriptor {
		var ds []descriptor
		for _, p := range arrayNodes(c.AsConstructorDeclaration().Parameters) {
			ds = append(ds, paramDescriptor(p, program))
		}
		return ds
	}
	for _, c := range candidates {
		ps := paramsOf(c)
		exact := true
		for i, d := range ps {
			if d != argDescs[i] {
				exact = false
				break
			}
		}
		if exact {
			return c
		}
	}
	kindConforms := func(arg, param descriptor) bool {
		if arg == param {
			return true
		}
		argRef := arg[0] == 'L' || arg[0] == '['
		paramRef := param[0] == 'L' || param[0] == '['
		if argRef || paramRef {
			return argRef && paramRef
		}
		return strings.Contains(widensTo[arg], string(param))
	}
	var conforming []*Node
	for _, c := range candidates {
		ps := paramsOf(c)
		ok := true
		for i, d := range ps {
			if !kindConforms(argDescs[i], d) {
				ok = false
				break
			}
		}
		if ok {
			conforming = append(conforming, c)
		}
	}
	if len(conforming) <= 1 {
		if len(conforming) > 0 {
			return conforming[0]
		}
		return nil
	}
	if refs != nil {
		for _, c := range conforming {
			ps := paramsOf(c)
			proven := true
			for i, d := range ps {
				if argDescs[i] == d {
					continue
				}
				argType := refs.argTypes[i]
				paramType := refs.checker.ResolveType(c.AsConstructorDeclaration().Parameters.Nodes[i].AsParameter().Type, c)
				if argType.Kind == TypeKindError || paramType.Kind == TypeKindError {
					proven = false
					break
				}
				if !refs.checker.IsAssignableTo(argType, paramType) {
					proven = false
					break
				}
			}
			if proven {
				return c
			}
		}
	}
	return conforming[0]
}

// --- shared instruction/codegen data types ----------------------------------
