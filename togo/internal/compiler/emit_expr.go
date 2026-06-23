package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// isOctalLiteral reports a Java octal integer literal (^0[0-7]+$).
func isOctalLiteral(s string) bool {
	if len(s) < 2 || s[0] != '0' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '7' {
			return false
		}
	}
	return true
}

// emitNumericLiteral emits the constant load for a numeric literal's source text
// (JLS 3.10.1/3.10.2), returning its descriptor.
func (g *bodyGen) emitNumericLiteral(raw string) descriptor {
	text := strings.ReplaceAll(raw, "_", "")
	isHex := strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X")
	isBin := strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B")
	if isHex && strings.ContainsAny(text, "pP") {
		panic(unsupportedEmit{}) // hex floating-point literal
	}
	last := text[len(text)-1]
	if !isHex && !isBin && (last == 'f' || last == 'F') {
		v, _ := strconv.ParseFloat(text[:len(text)-1], 64)
		g.floatConst(v)
		g.push("F")
		return "F"
	}
	if !isHex && !isBin && (strings.ContainsAny(text, ".eE") || last == 'd' || last == 'D') {
		s := text
		if last == 'd' || last == 'D' {
			s = text[:len(text)-1]
		}
		v, _ := strconv.ParseFloat(s, 64)
		g.doubleConst(v)
		g.push("D")
		return "D"
	}
	if last == 'l' || last == 'L' {
		body := text[:len(text)-1]
		var u uint64
		switch {
		case isHex || isBin:
			u, _ = strconv.ParseUint(body, 0, 64)
		case isOctalLiteral(body):
			u, _ = strconv.ParseUint(body, 8, 64)
		default:
			u, _ = strconv.ParseUint(body, 10, 64)
		}
		g.longConst(int64(u))
		g.push("J")
		return "J"
	}
	var value int64
	switch {
	case isHex || isBin:
		u, _ := strconv.ParseUint(text, 0, 64)
		value = int64(int32(uint32(u))) // wrap to signed 32-bit (0xFFFFFFFF -> -1)
	case isOctalLiteral(text):
		u, _ := strconv.ParseUint(text, 8, 64)
		value = int64(u)
	default:
		value, _ = strconv.ParseInt(text, 10, 64)
	}
	g.intConst(int(value))
	g.push("I")
	return "I"
}

// emitExpr generates code for an expression, returning the descriptor of the
// value left on the stack.
func (g *bodyGen) emitExpr(node *Node) descriptor {
	// Fold compile-time constant expressions (JLS 15.28), as javac does.
	if node.Kind == BinaryExpression || node.Kind == PrefixUnaryExpression {
		if folded := FoldConstant(node); folded != nil {
			switch folded.Kind {
			case ConstLong:
				g.longConst(folded.Int)
				g.push("J")
				return "J"
			case ConstBool:
				if folded.Bool {
					g.code.u1(opIconst1)
				} else {
					g.code.u1(opIconst0)
				}
				g.push("I")
				return "Z"
			default:
				g.intConst(int(folded.Int))
				g.push("I")
				return "I"
			}
		}
	}
	switch node.Kind {
	case ParenthesizedExpression:
		return g.emitExpr(node.AsParenthesizedExpression().Expression)
	case NumericLiteral:
		return g.emitNumericLiteral(node.AsLiteralExpression().Value)
	case StringLiteral, TextBlockLiteral:
		g.ldc(g.cp.stringConst(node.AsLiteralExpression().Value))
		g.pushRef(stringDesc)
		return stringDesc
	case CharacterLiteral:
		cc := utf16.Encode([]rune(node.AsLiteralExpression().Value))
		g.intConst(int(cc[0]))
		g.push("I")
		return "C"
	case TrueKeyword:
		g.code.u1(0x04) // iconst_1
		g.push("I")
		return "Z"
	case FalseKeyword:
		g.code.u1(opIconst0)
		g.push("I")
		return "Z"
	case NullKeyword:
		g.code.u1(opAconstNull)
		g.pushRef(objectDesc)
		return objectDesc
	case ThisExpression:
		qualifier := node.AsThisExpression().Qualifier
		if qualifier != nil {
			qType := g.checker.GetTypeOfExpression(qualifier)
			if qType.Kind != TypeKindClass {
				panic(unsupportedEmit{})
			}
			qInternal := binaryName(qType.Symbol)
			g.emitImplicitReceiver(qInternal)
			return descOf(qInternal)
		}
		g.code.u1(opAload0)
		g.pushRef(descOf(g.thisInternalName))
		return descOf(g.thisInternalName)
	case SuperExpression:
		g.code.u1(opAload0)
		g.pushRef(descOf(g.thisInternalName))
		return descOf(g.thisInternalName)
	case Identifier:
		symbol := g.checker.ResolveName(node)
		if symbol != nil {
			if local, ok := g.locals[symbol]; ok {
				g.loadVar(int(local.slot), local.descriptor)
				g.push(local.descriptor)
				return local.descriptor
			}
			if capture, ok := g.opts.captureFields[symbol]; ok {
				g.code.u1(opAload0)
				g.pushRef(objectDesc)
				g.code.u1(opGetfield)
				g.code.u2(int(g.cp.fieldref(capture.ownerInternal, capture.fieldName, capture.descriptor)))
				g.pop(1)
				g.push(capture.descriptor)
				return capture.descriptor
			}
			if symbol.Flags&(SymbolFlagsField|SymbolFlagsEnumConstant) != 0 {
				fi := g.fieldInfoOf(symbol)
				ownerFi := fi
				ownerFi.owner = g.implicitRefOwner(fi)
				return g.erasedCheckcast(node, g.emitFieldRead(ownerFi, func() { g.emitImplicitReceiver(fi.owner) }))
			}
		}
		panic(unsupportedEmit{})
	case ObjectCreationExpression:
		return g.emitNew(node)
	case BinaryExpression:
		b := node.AsBinaryExpression()
		if b.OperatorToken == PlusToken && g.exprIsString(g.checker.GetTypeOfExpression(node)) {
			return g.emitStringConcat(node)
		}
		if isBooleanOperator(b.OperatorToken) {
			return g.emitBoolean(node)
		}
		return g.emitBinary(node)
	case PostfixUnaryExpression:
		return g.emitIncDec(node, "old")
	case PrefixUnaryExpression:
		u := node.AsPrefixUnaryExpression()
		if u.Operator == PlusPlusToken || u.Operator == MinusMinusToken {
			return g.emitIncDec(node, "new")
		}
		if u.Operator == ExclamationToken {
			return g.emitBoolean(node)
		}
		return g.emitPrefixUnary(node)
	case PropertyAccessExpression:
		access := node.AsPropertyAccessExpression()
		if access.Name.AsIdentifier().Text == "length" && g.checker.GetTypeOfExpression(access.Expression).Kind == TypeKindArray {
			g.emitExpr(access.Expression)
			g.code.u1(opArraylength)
			g.pop(1)
			g.push("I")
			return "I"
		}
		symbol := g.checker.ResolveName(access.Name)
		if symbol == nil || symbol.Flags&(SymbolFlagsField|SymbolFlagsEnumConstant) == 0 {
			panic(unsupportedEmit{})
		}
		return g.erasedCheckcast(node, g.emitFieldRead(g.fieldInfoOf(symbol), func() { g.emitExpr(access.Expression) }))
	case ArrayCreationExpression:
		return g.emitArrayCreation(node)
	case ElementAccessExpression:
		return g.emitElementAccess(node)
	case ConditionalExpression:
		return g.emitConditional(node)
	case SwitchExpression:
		return g.emitSwitchExpression(node)
	case LambdaExpression:
		return g.emitLambda(node)
	case MethodReferenceExpression:
		return g.emitMethodRef(node)
	case CallExpression:
		return g.emitCall(node)
	case CastExpression:
		return g.emitCast(node)
	case ClassLiteralExpression:
		return g.emitClassLiteral(node)
	case InstanceofExpression:
		return g.emitInstanceof(node)
	default:
		panic(unsupportedEmit{})
	}
}

// primitiveConversion maps a from->to numeric category pair to its conversion opcode.
var primitiveConversion = map[string]int{
	"IJ": opI2l, "IF": opI2f, "ID": opI2d,
	"JI": opL2i, "JF": opL2f, "JD": opL2d,
	"FI": opF2i, "FJ": opF2l, "FD": opF2d,
	"DI": opD2i, "DJ": opD2l, "DF": opD2f,
}

// convertPrimitive converts the stack top from one numeric category to another.
func (g *bodyGen) convertPrimitive(fromCat string, targetDescriptor descriptor) {
	targetCat := category(targetDescriptor) // B/C/S/Z/I all collapse to I
	if fromCat != targetCat {
		op, ok := primitiveConversion[fromCat+targetCat]
		if !ok {
			panic(unsupportedEmit{})
		}
		g.code.u1(op)
		g.pop(1)
		g.push(descriptor(targetCat)) // a defined op never targets "A"
	}
	switch targetDescriptor {
	case "B":
		g.code.u1(opI2b)
	case "C":
		g.code.u1(opI2c)
	case "S":
		g.code.u1(opI2s)
	}
}

// emitCast emits a cast expression (JLS 15.16).
func (g *bodyGen) emitCast(node *Node) descriptor {
	c := node.AsCastExpression()
	targetDescriptor := descriptorOf(c.Type, g.program, nil)
	if strings.IndexByte("BCDFIJSZ", targetDescriptor[0]) >= 0 {
		fromCat, ok := numericCategory(g.checker.GetTypeOfExpression(c.Expression))
		if !ok {
			panic(unsupportedEmit{})
		}
		g.emitExpr(c.Expression)
		g.convertPrimitive(fromCat, targetDescriptor)
		return targetDescriptor
	}
	g.emitExpr(c.Expression)
	bounds := arrayNodes(c.Bounds)
	for i := len(bounds) - 1; i >= 0; i-- {
		g.code.u1(opCheckcast)
		g.code.u2(int(g.cp.classInfo(classOperand(descriptorOf(bounds[i], g.program, nil)))))
	}
	g.code.u1(opCheckcast)
	g.code.u2(int(g.cp.classInfo(classOperand(targetDescriptor))))
	return targetDescriptor
}

const classDesc descriptor = "Ljava/lang/Class;"

// emitClassLiteral emits T.class (JLS 15.8.2).
func (g *bodyGen) emitClassLiteral(node *Node) descriptor {
	d := descriptorOf(node.AsClassLiteralExpression().Type, g.program, nil)
	c := d[0]
	if strings.IndexByte("BCDFIJSZ", c) >= 0 || d == "V" {
		var wrapper internalName
		if d == "V" {
			wrapper = "java/lang/Void"
		} else {
			w, ok := wrapperOf(string(c))
			if !ok {
				panic(unsupportedEmit{})
			}
			wrapper = w
		}
		g.code.u1(opGetstatic)
		g.code.u2(int(g.cp.fieldref(wrapper, "TYPE", classDesc)))
	} else {
		g.ldc(g.cp.classInfo(classOperand(d)))
	}
	g.push(classDesc)
	return classDesc
}

// emitInstanceof emits the instanceof operator (JLS 15.20.2).
func (g *bodyGen) emitInstanceof(node *Node) descriptor {
	in := node.AsInstanceofExpression()
	if in.Name != nil || in.Pattern != nil || in.Type == nil {
		panic(unsupportedEmit{})
	}
	g.emitExpr(in.Expression)
	d := descriptorOf(in.Type, g.program, nil)
	g.code.u1(opInstanceof)
	g.code.u2(int(g.cp.classInfo(classOperand(d))))
	g.pop(1)
	g.push("I")
	return "Z"
}

// emitEnumStaticCall emits the implicit static enum methods E.values() /
// E.valueOf(String); returns ("", false) when the call is neither.
func (g *bodyGen) emitEnumStaticCall(call *Node) (descriptor, bool) {
	c := call.AsCallExpression()
	callee := c.Expression
	var enumInternal internalName
	var mname string
	switch callee.Kind {
	case Identifier:
		// Unqualified values()/valueOf(...) inside the enum's own body.
		if enumDecl := enclosingEnumDecl(call); enumDecl != nil && enumDecl.Symbol != nil {
			enumInternal = binaryName(enumDecl.Symbol)
			mname = callee.AsIdentifier().Text
		}
	case PropertyAccessExpression:
		access := callee.AsPropertyAccessExpression()
		if access.Expression.Kind == Identifier {
			if recv := ResolveTypeEntityName(access.Expression, access.Expression, g.program); recv != nil && recv.Flags&SymbolFlagsEnum != 0 {
				enumInternal = binaryName(recv)
				mname = access.Name.AsIdentifier().Text
			}
		}
	}
	if enumInternal == "" || mname == "" {
		return "", false
	}
	args := arrayNodes(c.Arguments)
	if mname == "values" && len(args) == 0 {
		g.code.u1(opInvokestatic)
		g.code.u2(int(g.cp.methodref(string(enumInternal), "values", methodDescriptor("()[L"+string(enumInternal)+";"))))
		g.push(descriptor("[L" + string(enumInternal) + ";"))
		return descriptor("[L" + string(enumInternal) + ";"), true
	}
	if mname == "valueOf" && len(args) == 1 {
		g.coerce(g.emitExpr(args[0]), stringDesc)
		g.code.u1(opInvokestatic)
		g.code.u2(int(g.cp.methodref(string(enumInternal), "valueOf", methodDescriptor("(Ljava/lang/String;)L"+string(enumInternal)+";"))))
		g.pop(1)
		g.push(descOf(enumInternal))
		return descOf(enumInternal), true
	}
	return "", false
}

// enclosingEnumDecl returns the innermost enclosing type declaration if it is an
// enum, else nil - so an unqualified values()/valueOf() resolves to the enum's
// synthetic statics only inside the enum's own body.
func enclosingEnumDecl(node *Node) *Node {
	for n := node.Parent; n != nil; n = n.Parent {
		if isTypeDeclarationKind(n.Kind) {
			if n.Kind == EnumDeclaration {
				return n
			}
			return nil
		}
	}
	return nil
}

// --- anonymous class targeting -----------------------------------------------

// isBodyClassNode reports whether a node becomes its own Outer$N class: an
// anonymous class (new T(){...}) or an enum constant with a body (CONST {...}).
// javac numbers both in a single per-enclosing-type counter, by source position.
func isBodyClassNode(n *Node) bool {
	return (n.Kind == ObjectCreationExpression && n.AsObjectCreationExpression().ClassBody != nil) ||
		(n.Kind == EnumConstantDeclaration && n.AsEnumConstantDeclaration().ClassBody != nil)
}

// bodyClassName returns the Outer$N binary name of a body-bearing node: the
// enclosing type's binary name plus a 1-based index over all body-class nodes in
// that type, ordered by source position - javac's numbering.
func bodyClassName(node *Node, program *Program) internalName {
	program.GetGlobalIndex()
	var enclosing *Node
	for n := node.Parent; n != nil; n = n.Parent {
		if isTypeDeclarationKind(n.Kind) {
			enclosing = n
			break
		}
	}
	base := internalName("Anonymous")
	if enclosing != nil && enclosing.Symbol != nil {
		base = binaryName(enclosing.Symbol)
	}
	index := 0
	var count func(n *Node)
	count = func(n *Node) {
		if isBodyClassNode(n) && n.Pos <= node.Pos {
			index++
		}
		if n != enclosing && isTypeDeclarationKind(n.Kind) {
			return // own counter
		}
		n.ForEachChild(func(c *Node) bool {
			count(c)
			return false
		})
	}
	if enclosing != nil {
		count(enclosing)
	} else {
		count(node)
	}
	return internalName(fmt.Sprintf("%s$%d", base, index))
}

func anonymousClassName(node *Node, program *Program) internalName {
	return bodyClassName(node, program)
}

// enumBodyClassName returns the Outer$N binary name of an enum constant body.
func enumBodyClassName(node *Node, program *Program) internalName {
	return bodyClassName(node, program)
}

// anonTarget describes the supertype an anonymous class can be emitted against.
type anonTarget struct {
	superInternal     internalName
	interfaceInternal internalName // "" if none
	superParamDescs   []descriptor
	superThis0Desc    descriptor // "" if none
	superDecl         *Node      // ClassDeclaration, nil if not project-source
}

// superTakesThis0 reports whether OUR emission of the extended member class gives
// its constructor the leading enclosing-instance parameter.
func superTakesThis0(target *anonTarget, program *Program, checker *Checker) bool {
	return target.superDecl != nil && memberInnerThis0(target.superDecl, program, checker) != ""
}

// ctorParamDescs maps a constructor's parameters to their descriptors.
func ctorParamDescs(ctor *Node, program *Program) []descriptor {
	var out []descriptor
	if ctor == nil {
		return out
	}
	for _, p := range arrayNodes(ctor.AsConstructorDeclaration().Parameters) {
		out = append(out, paramDescriptor(p, program))
	}
	return out
}

// anonymousTarget returns the super/interface and super-constructor parameters for
// an anonymous class we can emit, or nil when unsupported.
func anonymousTarget(node *Node, program *Program) *anonTarget {
	oc := node.AsObjectCreationExpression()
	if oc.ClassBody == nil || oc.Type.Kind != TypeReference {
		return nil
	}
	for _, m := range oc.ClassBody.Nodes {
		if m.Kind != MethodDeclaration && (m.Kind != FieldDeclaration || isStaticDeclaration(m)) {
			return nil
		}
	}
	sym := ResolveTypeEntityName(oc.Type.AsTypeReference().TypeName, node, program)
	if sym == nil {
		return nil
	}
	args := arrayNodes(oc.Arguments)
	if sym.Flags&SymbolFlagsInterface != 0 {
		if len(args) > 0 {
			return nil
		}
		return &anonTarget{superInternal: "java/lang/Object", interfaceInternal: binaryName(sym)}
	}
	ctor := findConstructor(sym, len(args), nil, nil, nil)
	if len(args) > 0 && ctor == nil {
		return nil
	}
	superParamDescs := ctorParamDescs(ctor, program)
	declaration := symbolDecl(sym)
	superThis0Desc := descriptor("")
	if declaration != nil && declaration.Kind == ClassDeclaration && declaration.Parent != nil &&
		isTypeDeclarationKind(declaration.Parent.Kind) && !isStaticDeclaration(declaration) && declaration.Parent.Symbol != nil {
		superThis0Desc = descOf(binaryName(declaration.Parent.Symbol))
	}
	var superDecl *Node
	if declaration != nil && declaration.Kind == ClassDeclaration {
		superDecl = declaration
	}
	return &anonTarget{
		superInternal:   binaryName(sym),
		superParamDescs: superParamDescs,
		superThis0Desc:  superThis0Desc,
		superDecl:       superDecl,
	}
}

// --- emitNew + arrays --------------------------------------------------------

func joinDescs(parts ...[]descriptor) string {
	s := ""
	for _, p := range parts {
		for _, d := range p {
			s += string(d)
		}
	}
	return s
}

// emitNew emits class instance creation `new T(args)` (JLS 15.9).
func (g *bodyGen) emitNew(node *Node) descriptor {
	oc := node.AsObjectCreationExpression()
	args := arrayNodes(oc.Arguments)
	if oc.Qualifier != nil {
		if oc.ClassBody != nil {
			target := anonymousTarget(node, g.program)
			if target == nil || target.superThis0Desc == "" {
				panic(unsupportedEmit{})
			}
			if !superTakesThis0(target, g.program, g.checker) {
				panic(unsupportedEmit{})
			}
			captures := collectCaptures(oc.ClassBody.Nodes, node.Pos, node.End, g.program, g.checker)
			outerThis := outerThisInfo(oc.ClassBody.Nodes, node.Parent, g.program, g.checker)
			if len(captures) > 0 || outerThis != "" {
				panic(unsupportedEmit{})
			}
			anonName := anonymousClassName(node, g.program)
			ref := descOf(anonName)
			g.code.u1(opNew)
			g.code.u2(int(g.cp.classInfo(string(anonName))))
			g.pushRef(ref)
			g.code.u1(opDup)
			g.pushRef(ref)
			g.emitExpr(oc.Qualifier)
			g.code.u1(opDup)
			g.pushRef(target.superThis0Desc)
			g.code.u1(opInvokestatic)
			g.code.u2(int(g.cp.methodref("java/util/Objects", "requireNonNull", "(Ljava/lang/Object;)Ljava/lang/Object;")))
			g.code.u1(opPop)
			g.pop(1)
			for i, arg := range args {
				g.coerce(g.emitExpr(arg), target.superParamDescs[i])
			}
			ctorDesc := methodDescriptor("(" + string(target.superThis0Desc) + joinDescs(target.superParamDescs) + ")V")
			g.code.u1(opInvokespecial)
			g.code.u2(int(g.cp.methodref(string(anonName), "<init>", ctorDesc)))
			g.pop(1 + 1 + len(args))
			return ref
		}
		created := g.checker.GetTypeOfExpression(node)
		if created.Kind != TypeKindClass {
			panic(unsupportedEmit{})
		}
		createdDecl := symbolDecl(created.Symbol)
		if createdDecl == nil || createdDecl.Kind != ClassDeclaration {
			panic(unsupportedEmit{})
		}
		inner := memberInnerThis0(createdDecl, g.program, g.checker)
		if inner == "" {
			panic(unsupportedEmit{})
		}
		owner := binaryName(created.Symbol)
		descs, types := g.ctorArgInfo(args)
		ctor := findConstructor(created.Symbol, len(args), g.program, descs, &findCtorRefs{checker: g.checker, argTypes: types})
		if ctor == nil && len(args) > 0 {
			panic(unsupportedEmit{})
		}
		ctorParams := ctorParamDescs(ctor, g.program)
		this0Desc := descOf(inner)
		ref := descOf(owner)
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(owner))))
		g.pushRef(ref)
		g.code.u1(opDup)
		g.pushRef(ref)
		g.emitExpr(oc.Qualifier)
		g.code.u1(opDup)
		g.pushRef(this0Desc)
		g.code.u1(opInvokestatic)
		g.code.u2(int(g.cp.methodref("java/util/Objects", "requireNonNull", "(Ljava/lang/Object;)Ljava/lang/Object;")))
		g.code.u1(opPop)
		g.pop(1)
		for i, arg := range args {
			d := g.emitExpr(arg)
			if i < len(ctorParams) {
				g.coerce(d, ctorParams[i])
			}
		}
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(owner), "<init>", methodDescriptor("("+string(this0Desc)+joinDescs(ctorParams)+")V"))))
		g.pop(1 + 1 + len(args))
		return ref
	}
	if oc.ClassBody != nil {
		target := anonymousTarget(node, g.program)
		if target == nil {
			panic(unsupportedEmit{})
		}
		if target.superThis0Desc != "" {
			panic(unsupportedEmit{})
		}
		anonName := anonymousClassName(node, g.program)
		captures := collectCaptures(oc.ClassBody.Nodes, node.Pos, node.End, g.program, g.checker)
		outerThis := outerThisInfo(oc.ClassBody.Nodes, node.Parent, g.program, g.checker)
		this0Desc := descriptor("")
		if outerThis != "" {
			this0Desc = descOf(outerThis)
		}
		ref := descOf(anonName)
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(anonName))))
		g.pushRef(ref)
		g.code.u1(opDup)
		g.pushRef(ref)
		if this0Desc != "" {
			g.code.u1(opAload0)
			g.pushRef(this0Desc)
		}
		for _, c := range captures {
			sl, ok := g.locals[c.symbol]
			if !ok {
				panic(unsupportedEmit{})
			}
			g.loadVar(int(sl.slot), c.descriptor)
			g.push(c.descriptor)
		}
		for i, arg := range args {
			g.coerce(g.emitExpr(arg), target.superParamDescs[i])
		}
		var captureDescs []descriptor
		for _, c := range captures {
			captureDescs = append(captureDescs, c.descriptor)
		}
		lead := ""
		nLead := 0
		if this0Desc != "" {
			lead = string(this0Desc)
			nLead = 1
		}
		ctorDesc := methodDescriptor("(" + lead + joinDescs(captureDescs, target.superParamDescs) + ")V")
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(anonName), "<init>", ctorDesc)))
		g.pop(1 + nLead + len(captures) + len(args))
		return ref
	}
	created := g.checker.GetTypeOfExpression(node)
	if created.Kind != TypeKindClass {
		panic(unsupportedEmit{})
	}
	owner := binaryName(created.Symbol)
	createdDecl := symbolDecl(created.Symbol)
	isLocal := createdDecl != nil && createdDecl.Kind == ClassDeclaration
	var captures []localCapture
	localThis0 := internalName("")
	if isLocal {
		captures = effectiveLocalCaptures(createdDecl, g.program, g.checker)
		localThis0 = localOuterThis(createdDecl, g.program, g.checker)
	}
	this0Desc := descriptor("")
	if localThis0 != "" {
		this0Desc = descOf(localThis0)
	}
	if len(captures) > 0 || this0Desc != "" {
		descs, types := g.ctorArgInfo(args)
		localCtor := findConstructor(created.Symbol, len(args), g.program, descs, &findCtorRefs{checker: g.checker, argTypes: types})
		if localCtor == nil && len(args) > 0 {
			panic(unsupportedEmit{})
		}
		userParamDescs := ctorParamDescs(localCtor, g.program)
		ref := descOf(owner)
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(owner))))
		g.pushRef(ref)
		g.code.u1(opDup)
		g.pushRef(ref)
		if this0Desc != "" {
			g.code.u1(opAload0)
			g.pushRef(this0Desc)
		}
		for _, c := range captures {
			sl, ok := g.locals[c.symbol]
			if !ok {
				panic(unsupportedEmit{})
			}
			g.loadVar(int(sl.slot), c.descriptor)
			g.push(c.descriptor)
		}
		for i, arg := range args {
			d := g.emitExpr(arg)
			if i < len(userParamDescs) {
				g.coerce(d, userParamDescs[i])
			}
		}
		var captureDescs []descriptor
		for _, c := range captures {
			captureDescs = append(captureDescs, c.descriptor)
		}
		lead := ""
		nLead := 0
		if this0Desc != "" {
			lead = string(this0Desc)
			nLead = 1
		}
		ctorDesc := methodDescriptor("(" + lead + joinDescs(captureDescs, userParamDescs) + ")V")
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(owner), "<init>", ctorDesc)))
		g.pop(1 + nLead + len(captures) + len(args))
		return ref
	}
	memberThis0 := internalName("")
	if isLocal {
		memberThis0 = memberInnerThis0(createdDecl, g.program, g.checker)
	}
	if memberThis0 != "" {
		descs, types := g.ctorArgInfo(args)
		innerCtor := findConstructor(created.Symbol, len(args), g.program, descs, &findCtorRefs{checker: g.checker, argTypes: types})
		if innerCtor == nil && len(args) > 0 {
			panic(unsupportedEmit{})
		}
		ctorParams := ctorParamDescs(innerCtor, g.program)
		this0D := descOf(memberThis0)
		ref := descOf(owner)
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(owner))))
		g.pushRef(ref)
		g.code.u1(opDup)
		g.pushRef(ref)
		g.emitImplicitReceiver(memberThis0)
		for i, arg := range args {
			d := g.emitExpr(arg)
			if i < len(ctorParams) {
				g.coerce(d, ctorParams[i])
			}
		}
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(owner), "<init>", methodDescriptor("("+string(this0D)+joinDescs(ctorParams)+")V"))))
		g.pop(1 + 1 + len(args))
		return ref
	}
	descs, types := g.ctorArgInfo(args)
	ctor := findConstructor(created.Symbol, len(args), g.program, descs, &findCtorRefs{checker: g.checker, argTypes: types})
	var recordDecl *Node
	if createdDecl != nil && createdDecl.Kind == RecordDeclaration {
		recordDecl = createdDecl
	}
	var ctorParams []descriptor
	switch {
	case ctor != nil:
		ctorParams = ctorParamDescs(ctor, g.program)
	case recordDecl != nil:
		hasCtor := false
		for _, m := range arrayNodes(recordDecl.AsRecordDeclaration().Members) {
			if m.Kind == ConstructorDeclaration {
				hasCtor = true
				break
			}
		}
		if !hasCtor {
			for _, c := range arrayNodes(recordDecl.AsRecordDeclaration().RecordComponents) {
				ctorParams = append(ctorParams, descriptorOf(c.AsRecordComponent().Type, g.program, nil))
			}
		}
	}
	if ctor == nil && recordDecl == nil && len(args) > 0 {
		panic(unsupportedEmit{})
	}
	ctorDescriptor := methodDescriptor("(" + joinDescs(ctorParams) + ")V")
	ref := descOf(owner)
	g.code.u1(opNew)
	g.code.u2(int(g.cp.classInfo(string(owner))))
	g.pushRef(ref)
	g.code.u1(opDup)
	g.pushRef(ref)
	for i, arg := range args {
		d := g.emitExpr(arg)
		if i < len(ctorParams) {
			g.coerce(d, ctorParams[i])
		}
	}
	g.code.u1(opInvokespecial)
	g.code.u2(int(g.cp.methodref(string(owner), "<init>", ctorDescriptor)))
	g.pop(1 + len(args))
	return ref
}

// arrayElemOffset is the xaload/xastore opcode offset for an element descriptor.
func arrayElemOffset(elem descriptor) int {
	switch elem[0] {
	case 'J':
		return 1
	case 'F':
		return 2
	case 'D':
		return 3
	case 'L', '[':
		return 4
	case 'Z', 'B':
		return 5
	case 'C':
		return 6
	case 'S':
		return 7
	default:
		return 0 // int
	}
}

var newarrayAtypeMap = map[descriptor]int{"Z": 4, "C": 5, "F": 6, "D": 7, "B": 8, "S": 9, "I": 10, "J": 11}

func (g *bodyGen) allocArray(elem descriptor) descriptor {
	if atype, ok := newarrayAtypeMap[elem]; ok {
		g.code.u1(opNewarray)
		g.code.u1(atype)
	} else {
		g.code.u1(opAnewarray)
		g.code.u2(int(g.cp.classInfo(classOperand(elem))))
	}
	g.pop(1)
	g.push(descriptor("[" + string(elem)))
	return descriptor("[" + string(elem))
}

func (g *bodyGen) arrayInitializer(init *Node, elem descriptor) descriptor {
	elements := arrayNodes(init.AsArrayInitializer().Elements)
	g.intConst(len(elements))
	g.push("I")
	arrDesc := g.allocArray(elem)
	for i, el := range elements {
		g.code.u1(opDup)
		g.push(arrDesc)
		g.intConst(i)
		g.push("I")
		if el.Kind == ArrayInitializer {
			g.arrayInitializer(el, elem[1:])
		} else {
			g.coerce(g.emitExpr(el), elem)
		}
		g.code.u1(opIastore + arrayElemOffset(elem))
		g.pop(3)
	}
	return arrDesc
}

func (g *bodyGen) packVarargs(elem descriptor, args []*Node) descriptor {
	g.intConst(len(args))
	g.push("I")
	arrDesc := g.allocArray(elem)
	for i, arg := range args {
		g.code.u1(opDup)
		g.push(arrDesc)
		g.intConst(i)
		g.push("I")
		g.coerce(g.emitExpr(arg), elem)
		g.code.u1(opIastore + arrayElemOffset(elem))
		g.pop(3)
	}
	return arrDesc
}

func (g *bodyGen) emitArrayCreation(node *Node) descriptor {
	n := node.AsArrayCreationExpression()
	elementType := descriptorOf(n.ElementType, g.program, nil)
	dimensions := arrayNodes(n.Dimensions)
	arrDesc := descriptor(strings.Repeat("[", len(dimensions)+n.AdditionalRank) + string(elementType))
	if n.Initializer != nil {
		return g.arrayInitializer(n.Initializer, arrDesc[1:])
	}
	if len(dimensions) == 1 {
		g.coerce(g.emitExpr(dimensions[0]), "I")
		return g.allocArray(arrDesc[1:])
	}
	for _, dim := range dimensions {
		g.coerce(g.emitExpr(dim), "I")
	}
	g.code.u1(opMultianewarray)
	g.code.u2(int(g.cp.classInfo(string(arrDesc))))
	g.code.u1(len(dimensions))
	g.pop(len(dimensions))
	g.push(arrDesc)
	return arrDesc
}

func (g *bodyGen) emitElementAccess(node *Node) descriptor {
	n := node.AsElementAccessExpression()
	arrDesc := g.emitExpr(n.Expression)
	elem := objectDesc
	if arrDesc[0] == '[' {
		elem = arrDesc[1:]
	}
	g.coerce(g.emitExpr(n.ArgumentExpression), "I")
	g.code.u1(opIaload + arrayElemOffset(elem))
	g.pop(2)
	g.push(elem)
	return elem
}

// --- numeric helpers + binary operators -------------------------------------

func numericCategory(t *Type) (string, bool) {
	if t.Kind != TypeKindPrimitive {
		return "", false
	}
	switch t.Name {
	case "long":
		return "J", true
	case "float":
		return "F", true
	case "double":
		return "D", true
	case "byte", "short", "char", "boolean", "int":
		return "I", true
	default:
		return "", false // void
	}
}

func numericCat(t *Type) (string, bool) {
	if c, ok := numericCategory(t); ok {
		return c, true
	}
	if t.Kind == TypeKindClass {
		if um, ok := unboxOf(string(binaryName(t.Symbol))); ok {
			if um.prim == "J" || um.prim == "F" || um.prim == "D" {
				return string(um.prim), true
			}
			return "I", true
		}
	}
	return "", false
}

func promote(a, b string) string {
	switch {
	case a == "D" || b == "D":
		return "D"
	case a == "F" || b == "F":
		return "F"
	case a == "J" || b == "J":
		return "J"
	default:
		return "I"
	}
}

func typeOffset(cat string) int {
	switch cat {
	case "J":
		return 1
	case "F":
		return 2
	case "D":
		return 3
	default:
		return 0
	}
}

var arithmeticOps = map[SyntaxKind]int{
	PlusToken: opIadd, MinusToken: opIsub, AsteriskToken: opImul,
	SlashToken: opIdiv, PercentToken: opIrem, AmpersandToken: opIand,
	BarToken: opIor, CaretToken: opIxor,
}

func arithmeticOp(op SyntaxKind) (int, bool) { v, ok := arithmeticOps[op]; return v, ok }

var shiftOps = map[SyntaxKind]int{
	LessThanLessThanToken: opIshl, GreaterThanGreaterThanToken: opIshr,
	GreaterThanGreaterThanGreaterThanToken: opIushr,
}

func shiftOp(op SyntaxKind) (int, bool) { v, ok := shiftOps[op]; return v, ok }

// emitOperand emits an operand promoted to targetCat.
func (g *bodyGen) emitOperand(node *Node, targetCat string) {
	if targetCat == "J" && node.Kind == NumericLiteral {
		text := strings.ReplaceAll(node.AsLiteralExpression().Value, "_", "")
		if !strings.ContainsAny(text, ".eEfFdDlL") {
			var v int64
			if isOctalLiteral(text) {
				u, _ := strconv.ParseUint(text, 8, 64)
				v = int64(u)
			} else {
				v, _ = strconv.ParseInt(text, 10, 64)
			}
			g.longConst(v)
			g.push("J")
			return
		}
	}
	g.coerce(g.emitExpr(node), descriptor(targetCat))
}

func (g *bodyGen) exprIsString(t *Type) bool {
	return t.Kind == TypeKindClass && binaryName(t.Symbol) == "java/lang/String"
}

// emitStringConcat emits string concatenation via invokedynamic
// makeConcatWithConstants (JLS 15.18.1).
func (g *bodyGen) emitStringConcat(node *Node) descriptor {
	var operands []*Node
	var flatten func(n *Node)
	flatten = func(n *Node) {
		if n.Kind == BinaryExpression && n.AsBinaryExpression().OperatorToken == PlusToken && g.exprIsString(g.checker.GetTypeOfExpression(n)) {
			flatten(n.AsBinaryExpression().Left)
			flatten(n.AsBinaryExpression().Right)
		} else {
			operands = append(operands, n)
		}
	}
	flatten(node)
	desc := ""
	for _, operand := range operands {
		desc += string(typeToDescriptor(g.checker.GetTypeOfExpression(operand), 0))
		g.emitExpr(operand)
	}
	g.code.u1(opInvokeDynamic)
	g.code.u2(int(g.cp.invokeDynamicConcat(strings.Repeat("\x01", len(operands)), desc)))
	g.code.u2(0)
	g.pop(len(operands))
	g.push(stringDesc)
	return stringDesc
}

// emitBinary emits a non-boolean binary operator (JLS 15.17-15.22).
func (g *bodyGen) emitBinary(node *Node) descriptor {
	n := node.AsBinaryExpression()
	op := n.OperatorToken
	lc, lok := numericCat(g.checker.GetTypeOfExpression(n.Left))
	rc, rok := numericCat(g.checker.GetTypeOfExpression(n.Right))
	if !lok || !rok {
		panic(unsupportedEmit{})
	}
	if shift, ok := shiftOp(op); ok {
		longShift := lc == "J"
		g.emitOperand(n.Left, lc)
		rcat, ok := numericCat(g.checker.GetTypeOfExpression(n.Right))
		if !ok {
			rcat = "I"
		}
		g.emitOperand(n.Right, rcat)
		if rcat != "I" {
			g.convertPrimitive(rcat, "I")
		}
		extra := 0
		if longShift {
			extra = 1
		}
		g.code.u1(shift + extra)
		g.pop(1)
		if longShift {
			return "J"
		}
		return "I"
	}
	base, ok := arithmeticOp(op)
	if !ok {
		panic(unsupportedEmit{})
	}
	bitwise := base == opIand || base == opIor || base == opIxor
	if bitwise && (lc == "F" || lc == "D" || rc == "F" || rc == "D") {
		panic(unsupportedEmit{})
	}
	t := promote(lc, rc)
	g.emitOperand(n.Left, t)
	g.emitOperand(n.Right, t)
	g.code.u1(base + typeOffset(t))
	g.pop(2)
	g.push(descriptor(t))
	return descriptor(t)
}

// emitPrefixUnary emits unary +, -, ~ (JLS 15.15.3-15.15.5).
func (g *bodyGen) emitPrefixUnary(node *Node) descriptor {
	n := node.AsPrefixUnaryExpression()
	op := n.Operator
	if op == PlusToken {
		return g.emitExpr(n.Operand) // unary plus: no-op
	}
	c, ok := numericCategory(g.checker.GetTypeOfExpression(n.Operand))
	if !ok {
		panic(unsupportedEmit{})
	}
	if op == MinusToken {
		g.emitExpr(n.Operand)
		g.code.u1(opIneg + typeOffset(c))
		return descriptor(c)
	}
	if op == TildeToken {
		if c != "I" && c != "J" {
			panic(unsupportedEmit{})
		}
		g.emitExpr(n.Operand)
		if c == "I" {
			g.code.u1(opIconstM1)
			g.push("I")
			g.code.u1(opIxor)
			g.pop(2)
			g.push("I")
			return "I"
		}
		g.longConst(-1)
		g.push("J")
		g.code.u1(opLxor)
		g.pop(2)
		g.push("J")
		return "J"
	}
	panic(unsupportedEmit{})
}
