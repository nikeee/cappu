package compiler

import (
	"fmt"
	"sort"
	"unicode/utf16"
)

// emitReturn emits the return instruction for the method's return type (JLS 14.17).
func (g *bodyGen) emitReturn() {
	switch g.returnDescriptor[0] {
	case 'V':
		g.code.u1(opReturn)
	case 'J':
		g.code.u1(opLreturn)
	case 'D':
		g.code.u1(opDreturn)
	case 'F':
		g.code.u1(opFreturn)
	case 'L', '[':
		g.code.u1(opAreturn)
	default:
		g.code.u1(opIreturn)
	}
	g.reachable = false
}

// compoundBase maps a compound-assignment operator to its underlying binary operator.
var compoundBase = map[SyntaxKind]SyntaxKind{
	PlusEqualsToken: PlusToken, MinusEqualsToken: MinusToken, AsteriskEqualsToken: AsteriskToken,
	SlashEqualsToken: SlashToken, PercentEqualsToken: PercentToken, AmpersandEqualsToken: AmpersandToken,
	BarEqualsToken: BarToken, CaretEqualsToken: CaretToken, LessThanLessThanEqualsToken: LessThanLessThanToken,
	GreaterThanGreaterThanEqualsToken:            GreaterThanGreaterThanToken,
	GreaterThanGreaterThanGreaterThanEqualsToken: GreaterThanGreaterThanGreaterThanToken,
}

// combineCompound combines the target value already on the stack with rhs for `target op= rhs`.
func (g *bodyGen) combineCompound(targetDesc descriptor, baseOp SyntaxKind, rhsNode *Node) {
	tcat := category(targetDesc)
	if tcat == "A" {
		rhsDesc := g.emitExpr(rhsNode)
		g.code.u1(opInvokeDynamic)
		g.code.u2(int(g.cp.invokeDynamicConcat("\x01\x01", string(targetDesc)+string(rhsDesc))))
		g.code.u2(0)
		g.pop(2)
		g.push(stringDesc)
		return
	}
	if shift, ok := shiftOp(baseOp); ok {
		rcat, ok := numericCat(g.checker.GetTypeOfExpression(rhsNode))
		if !ok {
			rcat = "I"
		}
		g.emitOperand(rhsNode, rcat)
		if rcat != "I" {
			g.convertPrimitive(rcat, "I")
		}
		extra := 0
		if tcat == "J" {
			extra = 1
		}
		g.code.u1(shift + extra)
		g.pop(1)
		g.convertPrimitive(tcat, targetDesc)
		return
	}
	base, ok := arithmeticOp(baseOp)
	if !ok {
		panic(unsupportedEmit{})
	}
	rcat, ok := numericCategory(g.checker.GetTypeOfExpression(rhsNode))
	if !ok {
		rcat = "I"
	}
	p := promote(tcat, rcat)
	g.coerce(descriptor(tcat), descriptor(p))
	g.emitOperand(rhsNode, p)
	g.code.u1(base + typeOffset(p))
	g.pop(2)
	g.push(descriptor(p))
	g.convertPrimitive(p, targetDesc)
}

// emitStore stores into an assignable target (local / static field / instance
// field / array element). emitValue must leave the value to store on the stack.
func (g *bodyGen) emitStore(target *Node, needsCurrent bool, emitValue func(d descriptor, loadCurrent func())) {
	writeField := func(info fieldInfo, emitReceiver func()) {
		ref := func() { g.code.u2(int(g.cp.fieldref(info.owner, info.name, info.descriptor))) }
		if info.isStatic {
			emitValue(info.descriptor, func() {
				g.code.u1(opGetstatic)
				ref()
				g.push(info.descriptor)
			})
			g.code.u1(opPutstatic)
			ref()
			g.pop(1)
			return
		}
		emitReceiver()
		if needsCurrent {
			g.code.u1(opDup)
			g.push(g.stack[len(g.stack)-1])
		}
		emitValue(info.descriptor, func() {
			g.code.u1(opGetfield)
			ref()
			g.pop(1)
			g.push(info.descriptor)
		})
		g.code.u1(opPutfield)
		ref()
		g.pop(2)
	}

	switch target.Kind {
	case Identifier:
		symbol := g.checker.ResolveName(target)
		if symbol != nil {
			if local, ok := g.locals[symbol]; ok {
				emitValue(local.descriptor, func() {
					g.loadVar(int(local.slot), local.descriptor)
					g.push(local.descriptor)
				})
				g.storeVar(int(local.slot), local.descriptor)
				return
			}
			if capture, ok := g.opts.captureFields[symbol]; ok {
				writeField(fieldInfo{owner: capture.ownerInternal, name: capture.fieldName, descriptor: capture.descriptor, isStatic: false}, func() {
					g.code.u1(opAload0)
					g.pushRef(descOf(g.thisInternalName))
				})
				return
			}
			if symbol.Flags&SymbolFlagsField != 0 {
				fi := g.fieldInfoOf(symbol)
				ownerFi := fi
				ownerFi.owner = g.implicitRefOwner(fi)
				writeField(ownerFi, func() {
					if fi.isStatic {
						return
					}
					g.emitImplicitReceiver(fi.owner)
				})
				return
			}
		}
		panic(unsupportedEmit{})
	case PropertyAccessExpression:
		access := target.AsPropertyAccessExpression()
		symbol := g.checker.ResolveName(access.Name)
		if symbol == nil || symbol.Flags&SymbolFlagsField == 0 {
			panic(unsupportedEmit{})
		}
		writeField(g.fieldInfoOf(symbol), func() { g.emitExpr(access.Expression) })
		return
	case ElementAccessExpression:
		access := target.AsElementAccessExpression()
		arrDesc := g.emitExpr(access.Expression)
		elem := objectDesc
		if arrDesc[0] == '[' {
			elem = arrDesc[1:]
		}
		g.coerce(g.emitExpr(access.ArgumentExpression), "I")
		if needsCurrent {
			g.code.u1(opDup2)
			g.push(arrDesc)
			g.push("I")
		}
		emitValue(elem, func() {
			g.code.u1(opIaload + arrayElemOffset(elem))
			g.pop(2)
			g.push(elem)
		})
		g.code.u1(opIastore + arrayElemOffset(elem))
		g.pop(3)
		return
	default:
		panic(unsupportedEmit{})
	}
}

// emitAssignStatement emits `target = rhs` / `target op= rhs`.
func (g *bodyGen) emitAssignStatement(assign *Node) {
	a := assign.AsAssignmentExpression()
	op := a.OperatorToken
	baseOp, hasBase := compoundBase[op]
	if op != EqualsToken && !hasBase {
		panic(unsupportedEmit{})
	}
	g.emitStore(a.Left, op != EqualsToken, func(d descriptor, loadCurrent func()) {
		if op == EqualsToken {
			g.coerce(g.emitExpr(a.Right), d)
			return
		}
		loadCurrent()
		g.combineCompound(d, baseOp, a.Right)
	})
}

// compareOffset maps a comparison operator to its if_icmp<cond> family offset.
var compareOffsets = map[SyntaxKind]int{
	EqualsEqualsToken: 0, ExclamationEqualsToken: 1, LessThanToken: 2,
	GreaterThanEqualsToken: 3, GreaterThanToken: 4, LessThanEqualsToken: 5,
}

func compareOffset(op SyntaxKind) (int, bool) { v, ok := compareOffsets[op]; return v, ok }

var negatedOffset = []int{1, 0, 3, 2, 5, 4}

// emitBranch emits a branch to label taken when expr is true (whenTrue) or false.
func (g *bodyGen) emitBranch(expr *Node, lbl *label, whenTrue bool) {
	switch expr.Kind {
	case ParenthesizedExpression:
		g.emitBranch(expr.AsParenthesizedExpression().Expression, lbl, whenTrue)
		return
	case PrefixUnaryExpression:
		u := expr.AsPrefixUnaryExpression()
		if u.Operator == ExclamationToken {
			g.emitBranch(u.Operand, lbl, !whenTrue)
			return
		}
	case InstanceofExpression:
		io := expr.AsInstanceofExpression()
		if io.Pattern != nil && !whenTrue {
			rp := io.Pattern.AsRecordPattern()
			desc := descriptorOf(rp.Type, g.program, nil)
			if desc[0] != 'L' {
				panic(unsupportedEmit{})
			}
			internal := classOperand(desc)
			xDesc := g.emitExpr(io.Expression)
			tmp := g.allocSlot(xDesc)
			g.storeVar(tmp, xDesc)
			g.loadVar(tmp, xDesc)
			g.push(xDesc)
			g.code.u1(opInstanceof)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push("I")
			g.pop(1)
			g.branchTo(opIfeq, lbl)
			objSlot := g.allocSlot(desc)
			g.loadVar(tmp, xDesc)
			g.push(xDesc)
			g.code.u1(opCheckcast)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push(desc)
			g.storeVar(objSlot, desc)
			g.emitDeconstruct(rp.Type, objSlot, desc, arrayNodes(rp.Patterns), lbl)
			return
		}
		if io.Name != nil && io.Name.Symbol != nil && io.Type != nil && !whenTrue {
			desc := descriptorOf(io.Type, g.program, nil)
			internal := classOperand(desc)
			xDesc := g.emitExpr(io.Expression)
			tmp := slot(g.nextSlot)
			g.nextSlot += slotsOf(xDesc)
			if g.nextSlot > g.maxLocals {
				g.maxLocals = g.nextSlot
			}
			g.activeLocals = append(g.activeLocals, localSlotInfo{slot: tmp, descriptor: xDesc})
			g.storeVar(int(tmp), xDesc)
			g.loadVar(int(tmp), xDesc)
			g.push(xDesc)
			g.code.u1(opInstanceof)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push("I")
			g.pop(1)
			g.branchTo(opIfeq, lbl)
			tSlot := slot(g.nextSlot)
			g.nextSlot += slotsOf(desc)
			if g.nextSlot > g.maxLocals {
				g.maxLocals = g.nextSlot
			}
			g.activeLocals = append(g.activeLocals, localSlotInfo{slot: tSlot, descriptor: desc})
			g.locals[io.Name.Symbol] = localSlotInfo{slot: tSlot, descriptor: desc}
			g.loadVar(int(tmp), xDesc)
			g.push(xDesc)
			if desc != objectDesc {
				g.code.u1(opCheckcast)
				g.code.u2(int(g.cp.classInfo(internal)))
				g.pop(1)
				g.push(desc)
			}
			g.storeVar(int(tSlot), desc)
			return
		}
	case BinaryExpression:
		b := expr.AsBinaryExpression()
		op := b.OperatorToken
		if op == AmpersandAmpersandToken {
			if whenTrue {
				skip := g.newLabel()
				g.emitBranch(b.Left, skip, false)
				g.emitBranch(b.Right, lbl, true)
				g.placeLabel(skip)
			} else {
				g.emitBranch(b.Left, lbl, false)
				g.emitBranch(b.Right, lbl, false)
			}
			return
		}
		if op == BarBarToken {
			if whenTrue {
				g.emitBranch(b.Left, lbl, true)
				g.emitBranch(b.Right, lbl, true)
			} else {
				skip := g.newLabel()
				g.emitBranch(b.Left, skip, true)
				g.emitBranch(b.Right, lbl, false)
				g.placeLabel(skip)
			}
			return
		}
		if offset, ok := compareOffset(op); ok {
			isEquality := op == EqualsEqualsToken || op == ExclamationEqualsToken
			isNull := func(n *Node) bool { return n.Kind == NullKeyword }
			leftType := g.checker.GetTypeOfExpression(b.Left)
			rightType := g.checker.GetTypeOfExpression(b.Right)
			_, rawLc := numericCategory(leftType)
			_, rawRc := numericCategory(rightType)
			lc, lok := numericCat(leftType)
			rc, rok := numericCat(rightType)
			if isEquality && (isNull(b.Left) || isNull(b.Right)) {
				if isNull(b.Left) {
					g.emitExpr(b.Right)
				} else {
					g.emitExpr(b.Left)
				}
				eq := op == EqualsEqualsToken
				g.pop(1)
				if eq == whenTrue {
					g.branchTo(opIfnull, lbl)
				} else {
					g.branchTo(opIfnonnull, lbl)
				}
				return
			}
			if isEquality && !rawLc && !rawRc {
				g.emitExpr(b.Left)
				g.emitExpr(b.Right)
				eq := op == EqualsEqualsToken
				g.pop(2)
				if eq == whenTrue {
					g.branchTo(opIfAcmpeq, lbl)
				} else {
					g.branchTo(opIfAcmpne, lbl)
				}
				return
			}
			if lok && rok && lc == "I" && rc == "I" {
				g.emitOperand(b.Left, "I")
				g.emitOperand(b.Right, "I")
				g.pop(2)
				if whenTrue {
					g.branchTo(opIfIcmpeq+offset, lbl)
				} else {
					g.branchTo(opIfIcmpeq+negatedOffset[offset], lbl)
				}
				return
			}
			if lok && rok {
				t := promote(lc, rc)
				g.emitOperand(b.Left, t)
				g.emitOperand(b.Right, t)
				gVariant := op == LessThanToken || op == LessThanEqualsToken
				switch t {
				case "J":
					g.code.u1(opLcmp)
				case "F":
					if gVariant {
						g.code.u1(opFcmpg)
					} else {
						g.code.u1(opFcmpl)
					}
				default:
					if gVariant {
						g.code.u1(opDcmpg)
					} else {
						g.code.u1(opDcmpl)
					}
				}
				g.pop(2)
				if whenTrue {
					g.branchTo(opIfeq+offset, lbl)
				} else {
					g.branchTo(opIfeq+negatedOffset[offset], lbl)
				}
				return
			}
			panic(unsupportedEmit{})
		}
	}
	// Fall back: evaluate a boolean value (unboxing a Boolean) and branch on it.
	g.coerce(g.emitExpr(expr), "Z")
	g.pop(1)
	if whenTrue {
		g.branchTo(opIfeq+1, lbl) // ifne
	} else {
		g.branchTo(opIfeq, lbl) // ifeq
	}
}

func isBooleanOperator(op SyntaxKind) bool {
	if op == AmpersandAmpersandToken || op == BarBarToken {
		return true
	}
	_, ok := compareOffset(op)
	return ok
}

// emitBoolean materializes a boolean expression as an int 0/1 on the stack.
func (g *bodyGen) emitBoolean(expr *Node) descriptor {
	trueL := g.newLabel()
	contL := g.newLabel()
	g.emitBranch(expr, trueL, true)
	g.code.u1(opIconst0)
	g.push("I")
	g.branchTo(opGoto, contL)
	g.pop(1)
	g.placeLabel(trueL)
	g.code.u1(opIconst1)
	g.push("I")
	g.placeLabel(contL)
	return "Z"
}

// emitConditional emits a conditional expression c ? a : b (JLS 15.25).
func (g *bodyGen) emitConditional(node *Node) descriptor {
	n := node.AsConditionalExpression()
	tt := g.checker.GetTypeOfExpression(n.WhenTrue)
	ft := g.checker.GetTypeOfExpression(n.WhenFalse)
	lc, lok := numericCategory(tt)
	rc, rok := numericCategory(ft)
	refDesc := func() descriptor {
		dt := typeToDescriptor(tt, 0)
		df := typeToDescriptor(ft, 0)
		if dt == df {
			return dt
		}
		if dt == objectDesc {
			return df
		}
		if df == objectDesc {
			return dt
		}
		return typeToDescriptor(g.checker.GetTypeOfExpression(node), 0)
	}
	var desc descriptor
	if lok && rok {
		desc = descriptor(promote(lc, rc))
	} else {
		desc = refDesc()
	}
	elseL := g.newLabel()
	contL := g.newLabel()
	g.emitBranch(n.Condition, elseL, false)
	g.coerce(g.emitExpr(n.WhenTrue), desc)
	g.pop(1)
	g.push(desc)
	g.branchTo(opGoto, contL)
	g.pop(1)
	g.placeLabel(elseL)
	g.coerce(g.emitExpr(n.WhenFalse), desc)
	g.pop(1)
	g.push(desc)
	g.placeLabel(contL)
	return desc
}

func (g *bodyGen) allocSlot(d descriptor) int {
	s := g.nextSlot
	g.nextSlot += slotsOf(d)
	if g.nextSlot > g.maxLocals {
		g.maxLocals = g.nextSlot
	}
	g.activeLocals = append(g.activeLocals, localSlotInfo{slot: slot(s), descriptor: d})
	return s
}

func isVoidType(t *Type) bool { return t.Kind == TypeKindPrimitive && t.Name == "void" }

func descOfType(t *Type) descriptor {
	if isVoidType(t) {
		return "V"
	}
	return typeToDescriptor(t, 0)
}

// emitCall emits a method invocation (JLS 15.12).
func (g *bodyGen) emitCall(call *Node) descriptor {
	c := call.AsCallExpression()
	if d, ok := g.emitEnumStaticCall(call); ok {
		return d
	}
	args := arrayNodes(c.Arguments)
	if c.Expression.Kind == PropertyAccessExpression && len(args) == 0 {
		pa := c.Expression.AsPropertyAccessExpression()
		if pa.Name.AsIdentifier().Text == "clone" {
			recvType := g.checker.GetTypeOfExpression(pa.Expression)
			if recvType.Kind == TypeKindArray {
				arrDesc := typeToDescriptor(recvType, 0)
				g.emitExpr(pa.Expression)
				g.code.u1(opInvokevirtual)
				g.code.u2(int(g.cp.methodref(string(arrDesc), "clone", "()Ljava/lang/Object;")))
				g.pop(1)
				g.push(objectDesc)
				g.code.u1(opCheckcast)
				g.code.u2(int(g.cp.classInfo(string(arrDesc))))
				g.pop(1)
				g.push(arrDesc)
				return arrDesc
			}
		}
	}
	decl := g.checker.ResolveCall(call)
	if decl == nil || decl.Symbol == nil || decl.Symbol.Parent == nil {
		panic(unsupportedEmit{})
	}
	owner := decl.Symbol.Parent
	ownerName := binaryName(owner)
	isInterface := owner.Flags&SymbolFlagsInterface != 0
	staticCall := isStaticDeclaration(decl)
	desc := methodDescriptorOf(decl, g.program)
	callee := c.Expression
	isSuperCall := callee.Kind == PropertyAccessExpression && callee.AsPropertyAccessExpression().Expression.Kind == SuperExpression
	if !staticCall {
		switch {
		case isSuperCall:
			g.code.u1(opAload0)
			g.pushRef(descOf(g.thisInternalName))
		case callee.Kind == PropertyAccessExpression:
			g.emitExpr(callee.AsPropertyAccessExpression().Expression)
		case callee.Kind == Identifier:
			g.emitImplicitReceiver(ownerName)
		default:
			panic(unsupportedEmit{})
		}
	}
	paramDescs := parseParamDescriptors(desc)
	params := methodParameters(decl)
	isVarargs := len(params) > 0 && len(paramDescs) > 0 && params[len(params)-1].AsParameter().IsVarArgs
	pushedValues := 0
	if isVarargs {
		varargsArrayDesc := paramDescs[len(paramDescs)-1]
		fixedCount := len(paramDescs) - 1
		refArray := func(d descriptor) bool { return d[0] == '[' && (d[1] == 'L' || d[1] == '[') }
		exactArray := false
		if len(args) == len(paramDescs) && len(args) > 0 {
			argDesc := typeToDescriptor(g.checker.GetTypeOfExpression(args[len(args)-1]), 0)
			exactArray = argDesc == varargsArrayDesc || (refArray(argDesc) && refArray(varargsArrayDesc))
		}
		if exactArray {
			for i, arg := range args {
				g.coerce(g.emitExpr(arg), paramDescs[i])
			}
			pushedValues = len(paramDescs)
		} else {
			for i := 0; i < fixedCount; i++ {
				g.coerce(g.emitExpr(args[i]), paramDescs[i])
			}
			g.packVarargs(varargsArrayDesc[1:], args[fixedCount:])
			pushedValues = fixedCount + 1
		}
	} else {
		coerceArgs := len(args) == len(paramDescs)
		for i, arg := range args {
			d := g.emitExpr(arg)
			if coerceArgs {
				g.coerce(d, paramDescs[i])
			}
		}
		pushedValues = len(args)
	}
	argSlots := 0
	for _, d := range paramDescs {
		argSlots += slotsOf(d)
	}
	returnDesc := returnDescriptorOf(desc)
	declName := decl.AsMethodDeclaration().Name.AsIdentifier().Text
	switch {
	case staticCall:
		g.code.u1(opInvokestatic)
		if isInterface {
			g.code.u2(int(g.cp.interfaceMethodref(ownerName, declName, desc)))
		} else {
			g.code.u2(int(g.cp.methodref(string(ownerName), declName, desc)))
		}
		g.pop(pushedValues)
	case isSuperCall:
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(ownerName), declName, desc)))
		g.pop(pushedValues + 1)
	case isInterface:
		g.code.u1(opInvokeinterface)
		g.code.u2(int(g.cp.interfaceMethodref(ownerName, declName, desc)))
		g.code.u1(argSlots + 1)
		g.code.u1(0)
		g.pop(pushedValues + 1)
	default:
		g.code.u1(opInvokevirtual)
		g.code.u2(int(g.cp.methodref(string(ownerName), declName, desc)))
		g.pop(pushedValues + 1)
	}
	if returnDesc == "V" {
		return returnDesc
	}
	g.push(returnDesc)
	return g.erasedCheckcast(call, returnDesc)
}

// emitLambda lowers a lambda (JLS 15.27) to an invokedynamic bound by LambdaMetafactory.
func (g *bodyGen) emitLambda(node *Node) descriptor {
	info := g.checker.GetLambdaInfo(node)
	if info == nil {
		panic(unsupportedEmit{})
	}
	lambda := node.AsLambdaExpression()
	var captures []*Symbol
	seen := map[*Symbol]bool{}
	needsThis := false
	declStatic := func(sym *Symbol) bool {
		d := symbolDecl(sym)
		return d != nil && isStaticDeclaration(d)
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		switch n.Kind {
		case ThisExpression, SuperExpression:
			needsThis = true
			return
		case PropertyAccessExpression:
			walk(n.AsPropertyAccessExpression().Expression)
			return
		case Identifier:
			sym := g.checker.ResolveName(n)
			if sym != nil {
				if _, ok := g.locals[sym]; ok {
					if !seen[sym] {
						seen[sym] = true
						captures = append(captures, sym)
					}
				} else if sym.Flags&SymbolFlagsField != 0 && !g.fieldInfoOf(sym).isStatic {
					needsThis = true
				}
			}
			return
		case CallExpression:
			callee := n.AsCallExpression().Expression
			if callee.Kind == Identifier {
				m := g.checker.ResolveName(callee)
				if m != nil && m.Flags&SymbolFlagsMethod != 0 && !declStatic(m) {
					needsThis = true
				}
			} else {
				walk(callee)
			}
			for _, arg := range arrayNodes(n.AsCallExpression().Arguments) {
				walk(arg)
			}
			return
		default:
			n.ForEachChild(func(c *Node) bool {
				walk(c)
				return false
			})
		}
	}
	walk(lambda.Body)
	if needsThis && g.isStatic {
		panic(unsupportedEmit{})
	}
	var instParamDescs []descriptor
	for _, t := range info.InstParams {
		instParamDescs = append(instParamDescs, typeToDescriptor(t, 0))
	}
	instReturnDesc := descOfType(info.InstReturn)
	erasedP := ""
	for _, t := range info.ErasedParams {
		erasedP += string(typeToDescriptor(t, 0))
	}
	samErased := methodDescriptor("(" + erasedP + ")" + string(descOfType(info.ErasedReturn)))
	instantiated := methodDescriptor("(" + joinDescs(instParamDescs) + ")" + string(instReturnDesc))

	var implParams []paramSym
	for _, s := range captures {
		implParams = append(implParams, paramSym{symbol: s, descriptor: g.locals[s].descriptor})
	}
	lambdaParams := arrayNodes(lambda.Parameters)
	for i, p := range lambdaParams {
		sym := p.Symbol
		if sym == nil || i >= len(instParamDescs) {
			panic(unsupportedEmit{})
		}
		implParams = append(implParams, paramSym{symbol: sym, descriptor: instParamDescs[i]})
	}
	implName := fmt.Sprintf("lambda$%s$%d", g.enclosingName, g.lambdaCounter)
	g.lambdaCounter++
	var implParamDescs []descriptor
	for _, p := range implParams {
		implParamDescs = append(implParamDescs, p.descriptor)
	}
	implDescriptor := methodDescriptor("(" + joinDescs(implParamDescs) + ")" + string(instReturnDesc))
	*g.opts.lambdaMethods = append(*g.opts.lambdaMethods, emitLambdaMethod(
		lambdaImpl{name: implName, params: implParams, returnDescriptor: instReturnDesc, body: lambda.Body, isInstance: needsThis},
		g.cp, g.program, g.checker, g.thisInternalName, g.opts.lambdaMethods))

	thisDesc := descOf(g.thisInternalName)
	if needsThis {
		g.code.u1(opAload0)
		g.push(thisDesc)
	}
	for _, c := range captures {
		g.loadVar(int(g.locals[c].slot), g.locals[c].descriptor)
		g.push(g.locals[c].descriptor)
	}
	interfaceDesc := typeToDescriptor(info.InterfaceType, 0)
	dynamicArgs := ""
	if needsThis {
		dynamicArgs = string(thisDesc)
	}
	for _, c := range captures {
		dynamicArgs += string(g.locals[c].descriptor)
	}
	indyDescriptor := methodDescriptor("(" + dynamicArgs + ")" + string(interfaceDesc))
	refKind := refInvokeStatic
	if needsThis {
		refKind = refInvokeSpecial
	}
	idx := g.cp.invokeDynamicLambda(string(info.SamName), indyDescriptor, samErased, refKind, g.thisInternalName, implName, implDescriptor, instantiated, false)
	g.code.u1(opInvokeDynamic)
	g.code.u2(int(idx))
	g.code.u2(0)
	nThis := 0
	if needsThis {
		nThis = 1
	}
	g.pop(len(captures) + nThis)
	g.push(interfaceDesc)
	return interfaceDesc
}

// emitMethodRef emits a method reference (JLS 15.13).
func (g *bodyGen) emitMethodRef(node *Node) descriptor {
	info := g.checker.GetMethodRefInfo(node)
	if info == nil {
		panic(unsupportedEmit{})
	}
	ref := node.AsMethodReferenceExpression()
	var instParamDescs []descriptor
	for _, t := range info.InstParams {
		instParamDescs = append(instParamDescs, typeToDescriptor(t, 0))
	}
	erasedP := ""
	for _, t := range info.ErasedParams {
		erasedP += string(typeToDescriptor(t, 0))
	}
	samErased := methodDescriptor("(" + erasedP + ")" + string(descOfType(info.ErasedReturn)))
	instantiated := methodDescriptor("(" + joinDescs(instParamDescs) + ")" + string(descOfType(info.InstReturn)))
	interfaceDesc := typeToDescriptor(info.InterfaceType, 0)

	if info.Kind == "arrayConstructor" {
		arrayDesc := descriptorOf(ref.Expression.AsClassLiteralExpression().Type, g.program, nil)
		implName := fmt.Sprintf("lambda$%s$%d", g.enclosingName, g.lambdaCounter)
		g.lambdaCounter++
		*g.opts.lambdaMethods = append(*g.opts.lambdaMethods, emitArrayCtorRefMethod(g.cp, implName, arrayDesc))
		idx := g.cp.invokeDynamicLambda(string(info.SamName), methodDescriptor("()"+string(interfaceDesc)), samErased,
			refInvokeStatic, g.thisInternalName, implName, methodDescriptor("(I)"+string(arrayDesc)), instantiated, false)
		g.code.u1(opInvokeDynamic)
		g.code.u2(int(idx))
		g.code.u2(0)
		g.push(interfaceDesc)
		return interfaceDesc
	}

	ownerInternal := binaryName(info.OwnerSymbol)
	isInterface := info.OwnerSymbol.Flags&SymbolFlagsInterface != 0
	var refKind int
	var implName string
	var implDescriptor methodDescriptor
	dynamicArgs := descriptor("")
	if info.Kind == "constructor" {
		refKind = refNewInvokeSpecial
		implName = "<init>"
		var argDescs []descriptor
		for _, t := range info.InstParams {
			argDescs = append(argDescs, typeToDescriptor(t, 0))
		}
		ctor := findConstructor(info.OwnerSymbol, len(info.InstParams), g.program, argDescs, &findCtorRefs{checker: g.checker, argTypes: info.InstParams})
		implDescriptor = methodDescriptor("(" + joinDescs(ctorParamDescs(ctor, g.program)) + ")V")
	} else {
		decl := info.Target
		implName = decl.AsMethodDeclaration().Name.AsIdentifier().Text
		implDescriptor = methodDescriptorOf(decl, g.program)
		switch {
		case info.Kind == "static":
			refKind = refInvokeStatic
		case isInterface:
			refKind = refInvokeInterface
		default:
			refKind = refInvokeVirtual
		}
		if info.Kind == "bound" {
			dynamicArgs = g.emitExpr(ref.Expression)
		}
	}
	idx := g.cp.invokeDynamicLambda(string(info.SamName), methodDescriptor("("+string(dynamicArgs)+")"+string(interfaceDesc)), samErased,
		refKind, ownerInternal, implName, implDescriptor, instantiated, isInterface)
	g.code.u1(opInvokeDynamic)
	g.code.u2(int(idx))
	g.code.u2(0)
	if dynamicArgs != "" {
		g.pop(1)
	}
	g.push(interfaceDesc)
	return interfaceDesc
}

// emitEnumClinitPrologue constructs each enum constant and the $VALUES array.
func (g *bodyGen) emitEnumClinitPrologue(ec *enumClinit) {
	for _, c := range ec.constants {
		// A constant with a body is an instance of its E$N subclass; otherwise the
		// enum itself. The constructor descriptor (name, ordinal, user args) is the
		// same on either (E$N forwards to the enum's matching ctor).
		owner := ec.enumInternal
		if c.ownerInternal != "" {
			owner = c.ownerInternal
		}
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(owner))))
		g.pushRef(ec.selfDesc)
		g.code.u1(opDup)
		g.pushRef(ec.selfDesc)
		g.ldc(g.cp.stringConst(c.name))
		g.pushRef(stringDesc)
		g.intConst(c.ordinal)
		g.push("I")
		for j, arg := range c.args {
			pd := objectDesc
			if j < len(c.userParamDescs) {
				pd = c.userParamDescs[j]
			}
			g.coerce(g.emitExpr(arg), pd)
		}
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(owner), "<init>", c.ctorDescriptor)))
		g.pop(1 + 2 + len(c.args))
		g.code.u1(opPutstatic)
		g.code.u2(int(g.cp.fieldref(ec.enumInternal, c.name, ec.selfDesc)))
		g.pop(1)
	}
	g.intConst(len(ec.constants))
	g.push("I")
	g.code.u1(opAnewarray)
	g.code.u2(int(g.cp.classInfo(string(ec.enumInternal))))
	g.pop(1)
	g.push(ec.arrayDesc)
	for i, c := range ec.constants {
		g.code.u1(opDup)
		g.push(ec.arrayDesc)
		g.intConst(i)
		g.push("I")
		g.code.u1(opGetstatic)
		g.code.u2(int(g.cp.fieldref(ec.enumInternal, c.name, ec.selfDesc)))
		g.pushRef(ec.selfDesc)
		g.code.u1(opAastore)
		g.pop(3)
	}
	g.code.u1(opPutstatic)
	g.code.u2(int(g.cp.fieldref(ec.enumInternal, ec.valuesField, ec.arrayDesc)))
	g.pop(1)
}

// emitIncDec emits ++/-- (JLS 15.14.2 / 15.15.1). result is "discard", "old", or "new".
func (g *bodyGen) emitIncDec(expr *Node, result string) descriptor {
	var operator SyntaxKind
	var operand *Node
	if expr.Kind == PostfixUnaryExpression {
		pu := expr.AsPostfixUnaryExpression()
		operator, operand = pu.Operator, pu.Operand
	} else {
		pu := expr.AsPrefixUnaryExpression()
		operator, operand = pu.Operator, pu.Operand
	}
	if operator != PlusPlusToken && operator != MinusMinusToken {
		panic(unsupportedEmit{})
	}
	isInc := operator == PlusPlusToken
	addOp := func(cat string) int {
		base := PlusToken
		if !isInc {
			base = MinusToken
		}
		op, _ := arithmeticOp(base)
		return op + typeOffset(cat)
	}
	pushOne := func(cat string) {
		switch cat {
		case "J":
			g.longConst(1)
		case "F":
			g.floatConst(1)
		case "D":
			g.doubleConst(1)
		default:
			g.code.u1(opIconst1)
		}
		if cat == "J" || cat == "F" || cat == "D" {
			g.push(descriptor(cat))
		} else {
			g.push("I")
		}
	}
	if operand.Kind == Identifier {
		symbol := g.checker.ResolveName(operand)
		if symbol != nil {
			if local, ok := g.locals[symbol]; ok {
				desc := local.descriptor
				cat := category(desc)
				if cat == "A" {
					panic(unsupportedEmit{})
				}
				if desc == "I" {
					if result == "old" {
						g.loadVar(int(local.slot), desc)
						g.push(desc)
					}
					g.code.u1(opIinc)
					g.code.u1(int(local.slot))
					delta := 1
					if !isInc {
						delta = -1
					}
					g.code.u1(delta & 0xff)
					if result == "new" {
						g.loadVar(int(local.slot), desc)
						g.push(desc)
					}
					return desc
				}
				if result == "old" {
					g.loadVar(int(local.slot), desc)
					g.push(desc)
				}
				g.loadVar(int(local.slot), desc)
				g.push(desc)
				pushOne(cat)
				g.code.u1(addOp(cat))
				g.pop(2)
				g.push(descriptor(cat))
				g.convertPrimitive(cat, desc)
				if result == "new" {
					if slotsOf(desc) == 2 {
						g.code.u1(opDup2)
					} else {
						g.code.u1(opDup)
					}
					g.push(descriptor(cat))
				}
				g.storeVar(int(local.slot), desc)
				return desc
			}
		}
	}
	if result != "discard" {
		panic(unsupportedEmit{})
	}
	g.emitStore(operand, true, func(d descriptor, loadCurrent func()) {
		cat := category(d)
		if cat == "A" {
			panic(unsupportedEmit{})
		}
		loadCurrent()
		pushOne(cat)
		g.code.u1(addOp(cat))
		g.pop(2)
		g.push(descriptor(cat))
		g.convertPrimitive(cat, d)
	})
	return typeToDescriptor(g.checker.GetTypeOfExpression(operand), 0)
}

// emitStatementExpression emits an expression used as a statement (value discarded).
func (g *bodyGen) emitStatementExpression(expr *Node) {
	if expr.Kind == PostfixUnaryExpression {
		g.emitIncDec(expr, "discard")
		return
	}
	if expr.Kind == PrefixUnaryExpression {
		u := expr.AsPrefixUnaryExpression()
		if u.Operator == PlusPlusToken || u.Operator == MinusMinusToken {
			g.emitIncDec(expr, "discard")
			return
		}
	}
	if expr.Kind == AssignmentExpression {
		g.emitAssignStatement(expr)
		return
	}
	desc := g.emitExpr(expr)
	if desc != "V" {
		if slotsOf(desc) == 2 {
			g.code.u1(opPop2)
		} else {
			g.code.u1(opPop)
		}
		g.pop(1)
	}
}

// caseValue is the constant value of an integral/char case label.
func (g *bodyGen) caseValue(node *Node) int {
	if node.Kind == CharacterLiteral {
		cc := utf16.Encode([]rune(node.AsLiteralExpression().Value))
		return int(cc[0])
	}
	folded := FoldConstant(node)
	if folded == nil || folded.Kind != ConstInt {
		panic(unsupportedEmit{})
	}
	return int(folded.Int)
}

type switchCase struct {
	value int
	label *label
}

// emitSwitchInstr emits a tableswitch/lookupswitch (JVMS 6).
func (g *bodyGen) emitSwitchInstr(cases []switchCase, defaultLabel *label) {
	from := pc(g.code.length())
	n := len(cases)
	lo, hi := 0, 0
	if n > 0 {
		lo = cases[0].value
		hi = cases[n-1].value
	}
	tableCost := 4 + (hi - lo + 1) + 3*3
	lookupCost := 3 + 2*n + 3*n
	useTable := n > 0 && tableCost <= lookupCost
	wide := func(lbl *label) {
		g.wideFixups = append(g.wideFixups, fixup{at: pc(g.code.length()), from: from, label: lbl})
		g.code.u4(0)
		if !lbl.hasTargetStack {
			lbl.targetStack = append(lbl.targetStack[:0:0], g.stack...)
			lbl.hasTargetStack = true
		}
		if !lbl.hasAssignedAtTarget {
			lbl.assignedAtTarget = copyIntSet(g.assigned)
			lbl.hasAssignedAtTarget = true
		} else {
			lbl.assignedAtTarget = intersectIntSets(lbl.assignedAtTarget, g.assigned)
		}
	}
	if useTable {
		g.code.u1(opTableswitch)
		for g.code.length()%4 != 0 {
			g.code.u1(0)
		}
		wide(defaultLabel)
		g.code.u4(lo & 0xffffffff)
		g.code.u4(hi & 0xffffffff)
		byValue := map[int]*label{}
		for _, c := range cases {
			byValue[c.value] = c.label
		}
		for v := lo; v <= hi; v++ {
			if lbl, ok := byValue[v]; ok {
				wide(lbl)
			} else {
				wide(defaultLabel)
			}
		}
	} else {
		g.code.u1(opLookupswitch)
		for g.code.length()%4 != 0 {
			g.code.u1(0)
		}
		wide(defaultLabel)
		g.code.u4(n & 0xffffffff)
		for _, c := range cases {
			g.code.u4(c.value & 0xffffffff)
			wide(c.label)
		}
	}
	g.reachable = false
}

// switchDispatchResult is what emitSwitchDispatch returns.
type switchDispatchResult struct {
	clauseLabels []*label
	endL         *label
	base         []descriptor
}

// emitSwitchDispatch emits a switch selector and its dispatch (integral/string/
// enum), shared by switch statements and switch expressions.
func (g *bodyGen) emitSwitchDispatch(selector *Node, clauses []*Node, throwOnNoMatch bool) switchDispatchResult {
	for _, cl := range clauses {
		if cl.AsSwitchClause().Guard != nil {
			panic(unsupportedEmit{})
		}
	}
	selType := g.checker.GetTypeOfExpression(selector)
	isString := g.exprIsString(selType)
	var enumSym *Symbol
	if selType.Kind == TypeKindClass && selType.Symbol.Flags&SymbolFlagsEnum != 0 {
		enumSym = selType.Symbol
	}
	if !isString && enumSym == nil {
		if c, ok := numericCat(selType); !ok || c != "I" {
			panic(unsupportedEmit{})
		}
	}
	enumOrdinal := func(lab *Node) int {
		decl := symbolDecl(enumSym)
		i := -1
		if lab.Kind == Identifier {
			for k, c := range arrayNodes(decl.AsEnumDeclaration().EnumConstants) {
				if nodeName(c).AsIdentifier().Text == lab.AsIdentifier().Text {
					i = k
					break
				}
			}
		}
		if i < 0 {
			panic(unsupportedEmit{})
		}
		return i
	}

	endL := g.newLabel()
	clauseLabels := make([]*label, len(clauses))
	for i := range clauseLabels {
		clauseLabels[i] = g.newLabel()
	}
	hasDefault := false
	for _, cl := range clauses {
		if cl.AsSwitchClause().IsDefault {
			hasDefault = true
		}
	}
	var throwL *label
	if throwOnNoMatch && !hasDefault {
		throwL = g.newLabel()
	}
	defaultLabel := endL
	if throwL != nil {
		defaultLabel = throwL
	}
	for i, cl := range clauses {
		if cl.AsSwitchClause().IsDefault {
			defaultLabel = clauseLabels[i]
		}
	}

	if isString {
		selDesc := stringDesc
		g.emitExpr(selector)
		tmp := slot(g.nextSlot)
		g.nextSlot++
		if g.nextSlot > g.maxLocals {
			g.maxLocals = g.nextSlot
		}
		g.activeLocals = append(g.activeLocals, localSlotInfo{slot: tmp, descriptor: selDesc})
		g.storeVar(int(tmp), selDesc)
		for i, cl := range clauses {
			for _, lab := range arrayNodes(cl.AsSwitchClause().Labels) {
				if lab.Kind != StringLiteral {
					panic(unsupportedEmit{})
				}
				g.loadVar(int(tmp), selDesc)
				g.push(selDesc)
				g.ldc(g.cp.stringConst(lab.AsLiteralExpression().Value))
				g.push(selDesc)
				g.code.u1(opInvokevirtual)
				g.code.u2(int(g.cp.methodref("java/lang/String", "equals", "(Ljava/lang/Object;)Z")))
				g.pop(2)
				g.push("I")
				g.pop(1)
				g.branchTo(opIfeq+1, clauseLabels[i]) // ifne
			}
		}
		g.branchTo(opGoto, defaultLabel)
	} else {
		if enumSym != nil {
			g.emitExpr(selector)
			g.code.u1(opInvokevirtual)
			g.code.u2(int(g.cp.methodref("java/lang/Enum", "ordinal", "()I")))
			g.pop(1)
			g.push("I")
		} else {
			g.coerce(g.emitExpr(selector), "I")
		}
		g.pop(1)
		var cases []switchCase
		for i, cl := range clauses {
			for _, lab := range arrayNodes(cl.AsSwitchClause().Labels) {
				v := g.caseValue(lab)
				if enumSym != nil {
					v = enumOrdinal(lab)
				}
				cases = append(cases, switchCase{value: v, label: clauseLabels[i]})
			}
		}
		sort.Slice(cases, func(a, b int) bool { return cases[a].value < cases[b].value })
		g.emitSwitchInstr(cases, defaultLabel)
	}
	base := append([]descriptor(nil), g.stack...)
	if throwL != nil {
		g.setStack(base)
		g.placeLabel(throwL)
		err := internalName("java/lang/IncompatibleClassChangeError")
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(err))))
		g.pushRef(descOf(err))
		g.code.u1(opDup)
		g.pushRef(descOf(err))
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref(string(err), "<init>", "()V")))
		g.pop(1)
		g.code.u1(opAthrow)
		g.pop(1)
	}
	return switchDispatchResult{clauseLabels: clauseLabels, endL: endL, base: base}
}

func switchArrowExpr(cl *Node) *Node {
	c := cl.AsSwitchClause()
	stmts := arrayNodes(c.Statements)
	if c.IsArrow && len(stmts) == 1 && stmts[0].Kind == ExpressionStatement {
		return stmts[0].AsExpressionStatement().Expression
	}
	return nil
}

// switchResultDesc is the result type of a switch expression.
func (g *bodyGen) switchResultDesc(clauses []*Node) descriptor {
	var types []*Type
	var collectYields func(n *Node)
	collectYields = func(n *Node) {
		if n.Kind == YieldStatement {
			types = append(types, g.checker.GetTypeOfExpression(n.AsYieldStatement().Expression))
		}
		n.ForEachChild(func(c *Node) bool {
			if c.Kind != SwitchExpression {
				collectYields(c)
			}
			return false
		})
	}
	for _, cl := range clauses {
		if arrowExpr := switchArrowExpr(cl); arrowExpr != nil {
			types = append(types, g.checker.GetTypeOfExpression(arrowExpr))
		} else {
			for _, st := range arrayNodes(cl.AsSwitchClause().Statements) {
				collectYields(st)
			}
		}
	}
	allNumeric := len(types) > 0
	var cats []string
	for _, t := range types {
		c, ok := numericCategory(t)
		if !ok {
			allNumeric = false
			break
		}
		cats = append(cats, c)
	}
	if allNumeric {
		acc := cats[0]
		for _, c := range cats[1:] {
			acc = promote(acc, c)
		}
		return descriptor(acc)
	}
	for _, t := range types {
		d := typeToDescriptor(t, 0)
		if d != objectDesc {
			return d
		}
	}
	return objectDesc
}

// emitSwitchExpression emits a switch expression (JLS 14.11.2).
func (g *bodyGen) emitSwitchExpression(node *Node) descriptor {
	n := node.AsSwitchExpression()
	clauses := arrayNodes(n.Clauses)
	resultDesc := g.switchResultDesc(clauses)
	if isPatternSwitch(clauses) {
		g.emitPatternSwitch(n.Expression, clauses, resultDesc, true)
		return resultDesc
	}
	disp := g.emitSwitchDispatch(n.Expression, clauses, true)
	g.yieldTargets = append(g.yieldTargets, yieldTarget{label: disp.endL, desc: resultDesc})
	for i, cl := range clauses {
		g.setStack(disp.base)
		g.placeLabel(disp.clauseLabels[i])
		if arrowExpr := switchArrowExpr(cl); arrowExpr != nil {
			g.coerce(g.emitExpr(arrowExpr), resultDesc)
			g.branchTo(opGoto, disp.endL)
			g.pop(1)
		} else {
			g.inScope(func() bool {
				t := false
				for _, st := range arrayNodes(cl.AsSwitchClause().Statements) {
					t = g.emitStmt(st)
				}
				return t
			})
		}
	}
	g.yieldTargets = g.yieldTargets[:len(g.yieldTargets)-1]
	g.setStack(disp.base)
	g.push(resultDesc)
	g.placeLabel(disp.endL)
	return resultDesc
}

func isPatternSwitch(clauses []*Node) bool {
	for _, cl := range clauses {
		for _, l := range arrayNodes(cl.AsSwitchClause().Labels) {
			if l.Kind == TypePattern || l.Kind == RecordPattern {
				return true
			}
		}
	}
	return false
}

type recordComponent struct {
	name       string
	descriptor descriptor
}

// recordComponentsOf resolves a record TypeNode to its components, or nil.
func (g *bodyGen) recordComponentsOf(typeNode *Node) []recordComponent {
	if typeNode.Kind != TypeReference {
		return nil
	}
	sym := ResolveTypeEntityName(typeNode.AsTypeReference().TypeName, typeNode, g.program)
	if sym == nil || sym.Flags&SymbolFlagsRecord == 0 {
		return nil
	}
	decl := symbolDecl(sym)
	if decl == nil || decl.Kind != RecordDeclaration {
		return nil
	}
	var out []recordComponent
	for _, c := range arrayNodes(decl.AsRecordDeclaration().RecordComponents) {
		rc := c.AsRecordComponent()
		out = append(out, recordComponent{name: rc.Name.AsIdentifier().Text, descriptor: descriptorOf(rc.Type, g.program, nil)})
	}
	return out
}

// bindComponent binds one component pattern (JLS 14.30.1).
func (g *bodyGen) bindComponent(pattern *Node, valueDesc descriptor, failLabel *label) {
	if pattern.Kind == MatchAllPattern {
		if slotsOf(valueDesc) == 2 {
			g.code.u1(opPop2)
		} else {
			g.code.u1(opPop)
		}
		g.pop(1)
		return
	}
	rawSlot := g.allocSlot(valueDesc)
	g.storeVar(rawSlot, valueDesc)
	if pattern.Kind == TypePattern {
		tp := pattern.AsTypePattern()
		desc := descriptorOf(tp.Type, g.program, nil)
		sym := pattern.Symbol
		if sym == nil && tp.Name != nil {
			sym = tp.Name.Symbol
		}
		if (desc[0] == 'L' || desc[0] == '[') && desc != valueDesc {
			internal := classOperand(desc)
			g.loadVar(rawSlot, valueDesc)
			g.push(valueDesc)
			g.code.u1(opInstanceof)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push("I")
			g.pop(1)
			g.branchTo(opIfeq, failLabel)
			sl := g.allocSlot(desc)
			g.loadVar(rawSlot, valueDesc)
			g.push(valueDesc)
			g.code.u1(opCheckcast)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push(desc)
			g.storeVar(sl, desc)
			if sym != nil {
				g.locals[sym] = localSlotInfo{slot: slot(sl), descriptor: desc}
			}
		} else if sym != nil {
			g.locals[sym] = localSlotInfo{slot: slot(rawSlot), descriptor: desc}
		}
		return
	}
	if pattern.Kind == RecordPattern {
		rp := pattern.AsRecordPattern()
		desc := descriptorOf(rp.Type, g.program, nil)
		if desc[0] != 'L' {
			panic(unsupportedEmit{})
		}
		internal := classOperand(desc)
		g.loadVar(rawSlot, valueDesc)
		g.push(valueDesc)
		g.code.u1(opInstanceof)
		g.code.u2(int(g.cp.classInfo(internal)))
		g.pop(1)
		g.push("I")
		g.pop(1)
		g.branchTo(opIfeq, failLabel)
		sl := g.allocSlot(desc)
		g.loadVar(rawSlot, valueDesc)
		g.push(valueDesc)
		g.code.u1(opCheckcast)
		g.code.u2(int(g.cp.classInfo(internal)))
		g.pop(1)
		g.push(desc)
		g.storeVar(sl, desc)
		g.emitDeconstruct(rp.Type, sl, desc, arrayNodes(rp.Patterns), failLabel)
		return
	}
	panic(unsupportedEmit{})
}

// emitDeconstruct deconstructs a record pattern against the value in objSlot.
func (g *bodyGen) emitDeconstruct(recordTypeNode *Node, objSlot int, objDesc descriptor, patterns []*Node, failLabel *label) {
	comps := g.recordComponentsOf(recordTypeNode)
	if comps == nil || len(comps) != len(patterns) {
		panic(unsupportedEmit{})
	}
	recordInternal := classOperand(objDesc)
	for i, p := range patterns {
		comp := comps[i]
		g.loadVar(objSlot, objDesc)
		g.push(objDesc)
		g.code.u1(opInvokevirtual)
		g.code.u2(int(g.cp.methodref(recordInternal, comp.name, methodDescriptor("()"+string(comp.descriptor)))))
		g.pop(1)
		g.push(comp.descriptor)
		g.bindComponent(p, comp.descriptor, failLabel)
	}
}

func (g *bodyGen) throwNew(internal internalName) {
	g.code.u1(opNew)
	g.code.u2(int(g.cp.classInfo(string(internal))))
	g.pushRef(descOf(internal))
	g.code.u1(opDup)
	g.pushRef(descOf(internal))
	g.code.u1(opInvokespecial)
	g.code.u2(int(g.cp.methodref(string(internal), "<init>", "()V")))
	g.pop(1)
	g.code.u1(opAthrow)
	g.pop(1)
	g.reachable = false
}

func blockStatements(node *Node) []*Node { return arrayNodes(node.AsBlock().Statements) }

// emitPatternSwitch lowers a pattern switch (JLS 14.11 / 14.30) to a null check
// plus an if/else-instanceof chain. hasResult => switch expression. Returns
// whether a statement form terminates.
func (g *bodyGen) emitPatternSwitch(selector *Node, clauses []*Node, resultDesc descriptor, hasResult bool) bool {
	selDesc := g.emitExpr(selector)
	tmpSlot := slot(g.nextSlot)
	g.nextSlot += slotsOf(selDesc)
	if g.nextSlot > g.maxLocals {
		g.maxLocals = g.nextSlot
	}
	g.activeLocals = append(g.activeLocals, localSlotInfo{slot: tmpSlot, descriptor: selDesc})
	g.storeVar(int(tmpSlot), selDesc)
	endL := g.newLabel()
	var nullClause, defaultClause *Node
	for _, cl := range clauses {
		c := cl.AsSwitchClause()
		for _, l := range arrayNodes(c.Labels) {
			if l.Kind == NullKeyword {
				nullClause = cl
			}
		}
		if c.IsDefault {
			defaultClause = cl
		}
	}

	emitArm := func(cl *Node) {
		if hasResult {
			if arrowExpr := switchArrowExpr(cl); arrowExpr != nil {
				g.coerce(g.emitExpr(arrowExpr), resultDesc)
				g.branchTo(opGoto, endL)
				g.pop(1)
			} else {
				g.inScope(func() bool {
					t := false
					for _, st := range arrayNodes(cl.AsSwitchClause().Statements) {
						t = g.emitStmt(st)
					}
					return t
				})
			}
		} else {
			term := g.inScope(func() bool {
				t := false
				for _, st := range arrayNodes(cl.AsSwitchClause().Statements) {
					t = g.emitStmt(st)
				}
				return t
			})
			if !term {
				g.branchTo(opGoto, endL)
			}
		}
	}

	if hasResult {
		g.yieldTargets = append(g.yieldTargets, yieldTarget{label: endL, desc: resultDesc})
	} else {
		g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack)})
	}

	afterNull := g.newLabel()
	g.loadVar(int(tmpSlot), selDesc)
	g.push(selDesc)
	g.pop(1)
	g.branchTo(opIfnonnull, afterNull)
	if nullClause != nil {
		emitArm(nullClause)
	} else {
		g.throwNew("java/lang/NullPointerException")
	}
	g.placeLabel(afterNull)

	for _, cl := range clauses {
		c := cl.AsSwitchClause()
		if c.IsDefault {
			continue
		}
		labels := arrayNodes(c.Labels)
		var lab *Node
		if len(labels) > 0 {
			lab = labels[0]
		}
		isType := lab != nil && lab.Kind == TypePattern
		isRecord := lab != nil && lab.Kind == RecordPattern
		if !isType && !isRecord {
			continue
		}
		var patternType *Node
		if isType {
			patternType = lab.AsTypePattern().Type
		} else {
			patternType = lab.AsRecordPattern().Type
		}
		desc := descriptorOf(patternType, g.program, nil)
		if desc[0] != 'L' && desc[0] != '[' {
			panic(unsupportedEmit{})
		}
		internal := classOperand(desc)
		nextL := g.newLabel()
		g.loadVar(int(tmpSlot), selDesc)
		g.push(selDesc)
		g.code.u1(opInstanceof)
		g.code.u2(int(g.cp.classInfo(internal)))
		g.pop(1)
		g.push("I")
		g.pop(1)
		g.branchTo(opIfeq, nextL)
		pSlot := slot(g.nextSlot)
		g.nextSlot += slotsOf(desc)
		if g.nextSlot > g.maxLocals {
			g.maxLocals = g.nextSlot
		}
		g.activeLocals = append(g.activeLocals, localSlotInfo{slot: pSlot, descriptor: desc})
		if isType {
			patternSym := lab.Symbol
			if patternSym == nil && lab.AsTypePattern().Name != nil {
				patternSym = lab.AsTypePattern().Name.Symbol
			}
			if patternSym != nil {
				g.locals[patternSym] = localSlotInfo{slot: pSlot, descriptor: desc}
			}
		}
		g.loadVar(int(tmpSlot), selDesc)
		g.push(selDesc)
		if desc != objectDesc {
			g.code.u1(opCheckcast)
			g.code.u2(int(g.cp.classInfo(internal)))
			g.pop(1)
			g.push(desc)
		}
		g.storeVar(int(pSlot), desc)
		if isRecord {
			g.emitDeconstruct(patternType, int(pSlot), desc, arrayNodes(lab.AsRecordPattern().Patterns), nextL)
		}
		if c.Guard != nil {
			g.emitBranch(c.Guard, nextL, false)
		}
		emitArm(cl)
		g.placeLabel(nextL)
	}

	if defaultClause != nil {
		emitArm(defaultClause)
	} else if hasResult {
		g.throwNew("java/lang/IncompatibleClassChangeError")
	}

	if hasResult {
		g.yieldTargets = g.yieldTargets[:len(g.yieldTargets)-1]
	} else {
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
	}

	if hasResult {
		g.setStack(nil)
		g.push(resultDesc)
		g.placeLabel(endL)
		return true
	}
	g.setStack(nil)
	g.placeLabel(endL)
	return false
}

// emitResourceClose emits a resource's close() (JLS 14.20.3).
func (g *bodyGen) emitResourceClose(a finallyAction) {
	desc := descOf(a.ownerInternal)
	var skip *label
	if a.guarded {
		g.loadVar(a.slot, desc)
		g.push(desc)
		g.pop(1)
		skip = g.newLabel()
		g.branchTo(opIfnull, skip)
	}
	g.loadVar(a.slot, desc)
	g.push(desc)
	if a.isInterface {
		g.code.u1(opInvokeinterface)
		g.code.u2(int(g.cp.interfaceMethodref(a.ownerInternal, "close", "()V")))
		g.code.u1(1)
		g.code.u1(0)
	} else {
		g.code.u1(opInvokevirtual)
		g.code.u2(int(g.cp.methodref(string(a.ownerInternal), "close", "()V")))
	}
	g.pop(1)
	if skip != nil {
		g.placeLabel(skip)
	}
}

// emitFinallyAction emits a finally action on a normal path; returns whether it terminates.
func (g *bodyGen) emitFinallyAction(a finallyAction) bool {
	switch a.kind {
	case "resource":
		g.emitResourceClose(a)
		return false
	case "monitor":
		g.loadVar(a.slot, objectDesc)
		g.push(objectDesc)
		g.code.u1(opMonitorexit)
		g.pop(1)
		return false
	default:
		term := false
		for _, st := range blockStatements(a.block) {
			term = g.emitStmt(st)
		}
		return term
	}
}

// runFinallies inlines the finally actions from the top down to toDepth.
func (g *bodyGen) runFinallies(toDepth int) bool {
	removed := append([]finallyAction(nil), g.finallyStack[toDepth:]...)
	g.finallyStack = g.finallyStack[:toDepth]
	aborted := false
	for i := len(removed) - 1; i >= 0 && !aborted; i-- {
		a := removed[i]
		aborted = g.inScope(func() bool { return g.emitFinallyAction(a) })
	}
	g.finallyStack = append(g.finallyStack, removed...)
	return aborted
}

// emitSuppressedClose emits the exceptional close of a resource (JLS 14.20.3).
func (g *bodyGen) emitSuppressedClose(a finallyAction, primarySlot int) {
	exc := throwableDesc
	bStart := pc(g.code.length())
	g.emitResourceClose(a)
	bEnd := pc(g.code.length())
	rethrowL := g.newLabel()
	g.branchTo(opGoto, rethrowL)
	assignedAtClose := copyIntSet(g.assigned)
	g.setStack(nil)
	g.assigned = assignedAtClose
	g.reachable = true
	g.push(exc)
	h2 := g.newLabel()
	g.placeLabel(h2)
	g.handlerOffsets = append(g.handlerOffsets, h2.offset)
	g.exceptionTable = append(g.exceptionTable, exceptionTableEntry{start: bStart, end: bEnd, handler: h2.offset, catchType: 0})
	sSlot := slot(g.nextSlot)
	g.nextSlot++
	if g.nextSlot > g.maxLocals {
		g.maxLocals = g.nextSlot
	}
	g.activeLocals = append(g.activeLocals, localSlotInfo{slot: sSlot, descriptor: exc})
	g.storeVar(int(sSlot), exc)
	g.loadVar(primarySlot, exc)
	g.push(exc)
	g.loadVar(int(sSlot), exc)
	g.push(exc)
	g.code.u1(opInvokevirtual)
	g.code.u2(int(g.cp.methodref("java/lang/Throwable", "addSuppressed", "(Ljava/lang/Throwable;)V")))
	g.pop(2)
	g.placeLabel(rethrowL)
}

// emitTryConstruct emits a try construct: a protected body, catch clauses, and an
// optional finally action (JLS 14.20.2 / 14.20.3).
func (g *bodyGen) emitTryConstruct(emitBody func() bool, catchClauses []*Node, fin *finallyAction) bool {
	endL := g.newLabel()
	reachesEnd := false
	tryStartAssigned := copyIntSet(g.assigned)
	type rangePc struct{ start, end pc }
	var protectedRanges []rangePc
	setEntryState := func() {
		g.setStack(nil)
		g.assigned = copyIntSet(tryStartAssigned)
		g.reachable = true
	}
	emitFinallyInline := func() bool { return g.inScope(func() bool { return g.emitFinallyAction(*fin) }) }
	completeNormally := func() {
		if fin != nil && emitFinallyInline() {
			return
		}
		reachesEnd = true
		g.branchTo(opGoto, endL)
	}

	tryStart := pc(g.code.length())
	if fin != nil {
		g.finallyStack = append(g.finallyStack, *fin)
	}
	tryTerm := emitBody()
	if fin != nil {
		g.finallyStack = g.finallyStack[:len(g.finallyStack)-1]
	}
	protectedRanges = append(protectedRanges, rangePc{start: tryStart, end: pc(g.code.length())})
	if !tryTerm {
		completeNormally()
	}

	for _, cc := range catchClauses {
		c := cc.AsCatchClause()
		setEntryState()
		catchTypes := arrayNodes(c.CatchTypes)
		excDesc := throwableDesc
		if len(catchTypes) == 1 {
			excDesc = descriptorOf(catchTypes[0], g.program, nil)
		}
		g.push(excDesc)
		handlerL := g.newLabel()
		g.placeLabel(handlerL)
		g.handlerOffsets = append(g.handlerOffsets, handlerL.offset)
		for _, ty := range catchTypes {
			d := descriptorOf(ty, g.program, nil)
			g.exceptionTable = append(g.exceptionTable, exceptionTableEntry{
				start: tryStart, end: protectedRanges[0].end, handler: handlerL.offset, catchType: g.cp.classInfo(classOperand(d)),
			})
		}
		bodyStart := pc(g.code.length())
		if fin != nil {
			g.finallyStack = append(g.finallyStack, *fin)
		}
		handlerTerm := g.inScope(func() bool {
			sl := slot(g.nextSlot)
			g.nextSlot += slotsOf(excDesc)
			if g.nextSlot > g.maxLocals {
				g.maxLocals = g.nextSlot
			}
			g.activeLocals = append(g.activeLocals, localSlotInfo{slot: sl, descriptor: excDesc})
			if c.Name.Symbol != nil {
				g.locals[c.Name.Symbol] = localSlotInfo{slot: sl, descriptor: excDesc}
			}
			g.storeVar(int(sl), excDesc)
			term := false
			for _, st := range blockStatements(c.Block) {
				term = g.emitStmt(st)
			}
			return term
		})
		if fin != nil {
			g.finallyStack = g.finallyStack[:len(g.finallyStack)-1]
		}
		protectedRanges = append(protectedRanges, rangePc{start: bodyStart, end: pc(g.code.length())})
		if !handlerTerm {
			completeNormally()
		}
	}

	if fin != nil {
		setEntryState()
		exc := throwableDesc
		g.push(exc)
		catchAllL := g.newLabel()
		g.placeLabel(catchAllL)
		g.handlerOffsets = append(g.handlerOffsets, catchAllL.offset)
		for _, r := range protectedRanges {
			g.exceptionTable = append(g.exceptionTable, exceptionTableEntry{start: r.start, end: r.end, handler: catchAllL.offset, catchType: 0})
		}
		sl := slot(g.nextSlot)
		g.nextSlot++
		if g.nextSlot > g.maxLocals {
			g.maxLocals = g.nextSlot
		}
		g.activeLocals = append(g.activeLocals, localSlotInfo{slot: sl, descriptor: exc})
		g.storeVar(int(sl), exc)
		finallyAborted := false
		if fin.kind == "resource" {
			g.emitSuppressedClose(*fin, int(sl))
		} else {
			finallyAborted = emitFinallyInline()
		}
		if !finallyAborted {
			g.loadVar(int(sl), exc)
			g.push(exc)
			g.code.u1(opAthrow)
			g.pop(1)
		}
		g.reachable = false
	}

	if reachesEnd {
		g.setStack(nil)
		g.placeLabel(endL)
	}
	return !reachesEnd
}

func (g *bodyGen) takePending() []string {
	p := g.pendingLabels
	g.pendingLabels = nil
	return p
}

func (g *bodyGen) reserveSlot(d descriptor) int {
	s := g.nextSlot
	g.nextSlot += slotsOf(d)
	if g.nextSlot > g.maxLocals {
		g.maxLocals = g.nextSlot
	}
	g.activeLocals = append(g.activeLocals, localSlotInfo{slot: slot(s), descriptor: d})
	return s
}

func (g *bodyGen) labelUsed(l *label) bool {
	for _, f := range g.fixups {
		if f.label == l {
			return true
		}
	}
	for _, f := range g.wideFixups {
		if f.label == l {
			return true
		}
	}
	return false
}

func findLastTarget(targets []branchTarget, name string) *branchTarget {
	for i := len(targets) - 1; i >= 0; i-- {
		for _, n := range targets[i].names {
			if n == name {
				return &targets[i]
			}
		}
	}
	return nil
}

// emitStmt emits a statement, returning true if it is a definite terminator.
func (g *bodyGen) emitStmt(stmt *Node) bool {
	g.recordLine(stmt)
	switch stmt.Kind {
	case Block:
		return g.inScope(func() bool {
			terminated := false
			for _, s := range blockStatements(stmt) {
				terminated = g.emitStmt(s)
			}
			return terminated
		})
	case EmptyStatement:
		return false
	case LocalVariableDeclarationStatement:
		decl := stmt.AsLocalVariableDeclarationStatement()
		for _, d := range arrayNodes(decl.Declarators) {
			declarator := d.AsVariableDeclarator()
			isVar := decl.Type.Kind == VarType
			if isVar && declarator.Initializer == nil {
				panic(unsupportedEmit{})
			}
			var desc descriptor
			if isVar {
				desc = typeToDescriptor(g.checker.GetTypeOfExpression(declarator.Initializer), 0)
			} else {
				desc = withRank(descriptorOf(decl.Type, g.program, nil), declarator.ArrayRankAfterName)
			}
			s := g.nextSlot
			g.nextSlot += slotsOf(desc)
			if g.nextSlot > g.maxLocals {
				g.maxLocals = g.nextSlot
			}
			if d.Symbol != nil {
				g.locals[d.Symbol] = localSlotInfo{slot: slot(s), descriptor: desc}
			}
			entryIdx := len(g.activeLocals)
			g.activeLocals = append(g.activeLocals, localSlotInfo{slot: slot(s), descriptor: desc})
			if declarator.Initializer != nil {
				if declarator.Initializer.Kind == ArrayInitializer && desc[0] == '[' {
					g.arrayInitializer(declarator.Initializer, desc[1:])
				} else {
					g.coerce(g.emitExpr(declarator.Initializer), desc)
				}
				g.storeVar(s, desc)
			}
			if emitDebugInfo {
				g.activeLocals[entryIdx].name = declarator.Name.AsIdentifier().Text
				g.activeLocals[entryIdx].lvtStart = pc(g.code.length())
				g.activeLocals[entryIdx].hasLvtStart = true
			}
		}
		return false
	case ExpressionStatement:
		g.emitStatementExpression(stmt.AsExpressionStatement().Expression)
		return false
	case ReturnStatement:
		expr := stmt.AsReturnStatement().Expression
		if expr != nil {
			g.coerce(g.emitExpr(expr), g.returnDescriptor)
		}
		if len(g.finallyStack) > 0 {
			if g.returnDescriptor != "V" {
				s := g.reserveSlot(g.returnDescriptor)
				g.storeVar(s, g.returnDescriptor)
				if g.runFinallies(0) {
					return true
				}
				g.loadVar(s, g.returnDescriptor)
				g.push(g.returnDescriptor)
			} else if g.runFinallies(0) {
				return true
			}
		}
		g.emitReturn()
		return true
	case IfStatement:
		s := stmt.AsIfStatement()
		if s.ElseStatement != nil {
			elseL := g.newLabel()
			endL := g.newLabel()
			g.emitBranch(s.Condition, elseL, false)
			thenTerm := g.inScope(func() bool { return g.emitStmt(s.ThenStatement) })
			if !thenTerm {
				g.branchTo(opGoto, endL)
			}
			g.placeLabel(elseL)
			elseTerm := g.inScope(func() bool { return g.emitStmt(s.ElseStatement) })
			terminated := thenTerm && elseTerm
			if !terminated {
				g.placeLabel(endL)
			}
			return terminated
		}
		endL := g.newLabel()
		g.emitBranch(s.Condition, endL, false)
		g.inScope(func() bool { return g.emitStmt(s.ThenStatement) })
		g.placeLabel(endL)
		return false
	case WhileStatement:
		s := stmt.AsWhileStatement()
		startL := g.newLabel()
		endL := g.newLabel()
		g.placeLabel(startL)
		g.emitBranch(s.Condition, endL, false)
		names := g.takePending()
		g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
		g.continueTargets = append(g.continueTargets, branchTarget{label: startL, finallyDepth: len(g.finallyStack), names: names})
		g.inScope(func() bool { return g.emitStmt(s.Statement) })
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
		g.continueTargets = g.continueTargets[:len(g.continueTargets)-1]
		g.branchTo(opGoto, startL)
		g.placeLabel(endL)
		return false
	case DoStatement:
		s := stmt.AsDoStatement()
		startL := g.newLabel()
		condL := g.newLabel()
		endL := g.newLabel()
		g.placeLabel(startL)
		names := g.takePending()
		g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
		g.continueTargets = append(g.continueTargets, branchTarget{label: condL, finallyDepth: len(g.finallyStack), names: names})
		g.inScope(func() bool { return g.emitStmt(s.Statement) })
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
		g.continueTargets = g.continueTargets[:len(g.continueTargets)-1]
		g.placeLabel(condL)
		g.emitBranch(s.Condition, startL, true)
		g.placeLabel(endL)
		return false
	case ForStatement:
		s := stmt.AsForStatement()
		return g.inScope(func() bool {
			if s.Initializer != nil {
				g.emitStmt(s.Initializer)
			}
			for _, e := range arrayNodes(s.InitializerExpressions) {
				g.emitStatementExpression(e)
			}
			startL := g.newLabel()
			stepL := g.newLabel()
			endL := g.newLabel()
			g.placeLabel(startL)
			if s.Condition != nil {
				g.emitBranch(s.Condition, endL, false)
			}
			names := g.takePending()
			g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
			g.continueTargets = append(g.continueTargets, branchTarget{label: stepL, finallyDepth: len(g.finallyStack), names: names})
			g.inScope(func() bool { return g.emitStmt(s.Statement) })
			g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
			g.continueTargets = g.continueTargets[:len(g.continueTargets)-1]
			g.placeLabel(stepL)
			for _, e := range arrayNodes(s.Incrementors) {
				g.emitStatementExpression(e)
			}
			g.branchTo(opGoto, startL)
			g.placeLabel(endL)
			return false
		})
	case ForEachStatement:
		return g.emitForEach(stmt)
	case ThrowStatement:
		g.emitExpr(stmt.AsThrowStatement().Expression)
		g.code.u1(opAthrow)
		g.pop(1)
		g.reachable = false
		return true
	case TryStatement:
		return g.emitTry(stmt)
	case SynchronizedStatement:
		s := stmt.AsSynchronizedStatement()
		monDesc := objectDesc
		monSlot := g.reserveSlot(monDesc)
		g.emitExpr(s.Expression)
		g.code.u1(opDup)
		g.push(monDesc)
		g.storeVar(monSlot, monDesc)
		g.code.u1(opMonitorenter)
		g.pop(1)
		return g.emitTryConstruct(func() bool { return g.inScope(func() bool { return g.emitStmt(s.Body) }) }, nil, &finallyAction{kind: "monitor", slot: monSlot})
	case YieldStatement:
		if len(g.yieldTargets) == 0 {
			panic(unsupportedEmit{})
		}
		target := g.yieldTargets[len(g.yieldTargets)-1]
		g.coerce(g.emitExpr(stmt.AsYieldStatement().Expression), target.desc)
		g.branchTo(opGoto, target.label)
		return true
	case BreakStatement:
		lbl := stmt.AsLabelStatement().Label
		var target *branchTarget
		if lbl != nil {
			target = findLastTarget(g.breakTargets, lbl.AsIdentifier().Text)
		} else if len(g.breakTargets) > 0 {
			target = &g.breakTargets[len(g.breakTargets)-1]
		}
		if target == nil {
			panic(unsupportedEmit{})
		}
		if g.runFinallies(target.finallyDepth) {
			return true
		}
		g.branchTo(opGoto, target.label)
		return true
	case ContinueStatement:
		lbl := stmt.AsLabelStatement().Label
		var target *branchTarget
		if lbl != nil {
			target = findLastTarget(g.continueTargets, lbl.AsIdentifier().Text)
		} else if len(g.continueTargets) > 0 {
			target = &g.continueTargets[len(g.continueTargets)-1]
		}
		if target == nil {
			panic(unsupportedEmit{})
		}
		if g.runFinallies(target.finallyDepth) {
			return true
		}
		g.branchTo(opGoto, target.label)
		return true
	case LabeledStatement:
		var names []string
		body := stmt
		for body.Kind == LabeledStatement {
			names = append(names, body.AsLabeledStatement().Label.AsIdentifier().Text)
			body = body.AsLabeledStatement().Statement
		}
		switch body.Kind {
		case WhileStatement, DoStatement, ForStatement, ForEachStatement:
			g.pendingLabels = append(g.pendingLabels, names...)
			return g.emitStmt(body)
		}
		endL := g.newLabel()
		g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
		term := g.inScope(func() bool { return g.emitStmt(body) })
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
		if g.labelUsed(endL) {
			g.placeLabel(endL)
			return false
		}
		return term
	case SwitchStatement:
		s := stmt.AsSwitchStatement()
		clauses := arrayNodes(s.Clauses)
		if isPatternSwitch(clauses) {
			return g.emitPatternSwitch(s.Expression, clauses, "", false)
		}
		disp := g.emitSwitchDispatch(s.Expression, clauses, false)
		g.breakTargets = append(g.breakTargets, branchTarget{label: disp.endL, finallyDepth: len(g.finallyStack)})
		arrow := false
		for _, cl := range clauses {
			if cl.AsSwitchClause().IsArrow {
				arrow = true
			}
		}
		lastTerminated := false
		for i, cl := range clauses {
			g.setStack(disp.base)
			g.placeLabel(disp.clauseLabels[i])
			term := g.inScope(func() bool {
				t := false
				for _, st := range arrayNodes(cl.AsSwitchClause().Statements) {
					t = g.emitStmt(st)
				}
				return t
			})
			if arrow && !term {
				g.branchTo(opGoto, disp.endL)
			}
			lastTerminated = term
		}
		g.setStack(disp.base)
		g.placeLabel(disp.endL)
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
		hasDefault := false
		for _, cl := range clauses {
			if cl.AsSwitchClause().IsDefault {
				hasDefault = true
			}
		}
		endBranched := g.labelUsed(disp.endL)
		return hasDefault && lastTerminated && !endBranched
	case AssertStatement:
		s := stmt.AsAssertStatement()
		endL := g.newLabel()
		g.code.u1(opGetstatic)
		g.code.u2(int(g.cp.fieldref(g.thisInternalName, "$assertionsDisabled", "Z")))
		g.push("I")
		g.pop(1)
		g.branchTo(opIfeq+1, endL) // ifne: assertions disabled -> skip
		g.emitBranch(s.Condition, endL, true)
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo("java/lang/AssertionError")))
		g.pushRef("Ljava/lang/AssertionError;")
		g.code.u1(opDup)
		g.pushRef("Ljava/lang/AssertionError;")
		ctorDesc := methodDescriptor("()V")
		if s.Message != nil {
			md := g.emitExpr(s.Message)
			g.coerce(md, objectDesc)
			ctorDesc = "(Ljava/lang/Object;)V"
		}
		g.code.u1(opInvokespecial)
		g.code.u2(int(g.cp.methodref("java/lang/AssertionError", "<init>", ctorDesc)))
		if s.Message != nil {
			g.pop(2)
		} else {
			g.pop(1)
		}
		g.code.u1(opAthrow)
		g.pop(1)
		g.reachable = false
		g.placeLabel(endL)
		return false
	case ClassDeclaration, InterfaceDeclaration, EnumDeclaration, RecordDeclaration:
		return false
	default:
		panic(unsupportedEmit{})
	}
}

// emitForEach emits the enhanced for statement (JLS 14.14.2).
func (g *bodyGen) emitForEach(stmt *Node) bool {
	s := stmt.AsForEachStatement()
	iterableType := g.checker.GetTypeOfExpression(s.Expression)
	param := s.Parameter
	pd := param.AsParameter()
	var varDesc descriptor
	if pd.Type != nil && pd.Type.Kind != VarType {
		varDesc = descriptorOf(pd.Type, g.program, nil)
	} else {
		varDesc = typeToDescriptor(g.checker.GetTypeOfSymbol(param.Symbol), 0)
	}
	if iterableType.Kind == TypeKindArray {
		return g.inScope(func() bool {
			arrDesc := g.emitExpr(s.Expression)
			elem := objectDesc
			if arrDesc[0] == '[' {
				elem = arrDesc[1:]
			}
			arrSlot := g.reserveSlot(arrDesc)
			g.storeVar(arrSlot, arrDesc)
			idxSlot := g.reserveSlot("I")
			g.code.u1(opIconst0)
			g.push("I")
			g.storeVar(idxSlot, "I")
			varSlot := g.reserveSlot(varDesc)
			if param.Symbol != nil {
				g.locals[param.Symbol] = localSlotInfo{slot: slot(varSlot), descriptor: varDesc}
			}
			startL := g.newLabel()
			stepL := g.newLabel()
			endL := g.newLabel()
			g.placeLabel(startL)
			g.loadVar(idxSlot, "I")
			g.push("I")
			g.loadVar(arrSlot, arrDesc)
			g.push(arrDesc)
			g.code.u1(opArraylength)
			g.pop(1)
			g.push("I")
			g.pop(2)
			g.branchTo(opIfIcmpeq+3, endL) // if_icmpge: $i >= length -> exit
			g.loadVar(arrSlot, arrDesc)
			g.push(arrDesc)
			g.loadVar(idxSlot, "I")
			g.push("I")
			g.code.u1(opIaload + arrayElemOffset(elem))
			g.pop(2)
			g.push(elem)
			g.coerce(elem, varDesc)
			g.storeVar(varSlot, varDesc)
			names := g.takePending()
			g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
			g.continueTargets = append(g.continueTargets, branchTarget{label: stepL, finallyDepth: len(g.finallyStack), names: names})
			g.inScope(func() bool { return g.emitStmt(s.Statement) })
			g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
			g.continueTargets = g.continueTargets[:len(g.continueTargets)-1]
			g.placeLabel(stepL)
			g.code.u1(opIinc)
			g.code.u1(idxSlot)
			g.code.u1(1)
			g.branchTo(opGoto, startL)
			g.placeLabel(endL)
			return false
		})
	}
	if iterableType.Kind != TypeKindClass {
		panic(unsupportedEmit{})
	}
	return g.inScope(func() bool {
		iter := internalName("java/util/Iterator")
		g.emitExpr(s.Expression)
		g.code.u1(opInvokeinterface)
		g.code.u2(int(g.cp.interfaceMethodref("java/lang/Iterable", "iterator", "()Ljava/util/Iterator;")))
		g.code.u1(1)
		g.code.u1(0)
		g.pop(1)
		g.push(iteratorDesc)
		itSlot := g.reserveSlot(iteratorDesc)
		g.storeVar(itSlot, iteratorDesc)
		varSlot := g.reserveSlot(varDesc)
		if param.Symbol != nil {
			g.locals[param.Symbol] = localSlotInfo{slot: slot(varSlot), descriptor: varDesc}
		}
		startL := g.newLabel()
		endL := g.newLabel()
		g.placeLabel(startL)
		g.loadVar(itSlot, iteratorDesc)
		g.push(iteratorDesc)
		g.code.u1(opInvokeinterface)
		g.code.u2(int(g.cp.interfaceMethodref(iter, "hasNext", "()Z")))
		g.code.u1(1)
		g.code.u1(0)
		g.pop(1)
		g.push("I")
		g.pop(1)
		g.branchTo(opIfeq, endL)
		g.loadVar(itSlot, iteratorDesc)
		g.push(iteratorDesc)
		g.code.u1(opInvokeinterface)
		g.code.u2(int(g.cp.interfaceMethodref(iter, "next", "()Ljava/lang/Object;")))
		g.code.u1(1)
		g.code.u1(0)
		g.pop(1)
		g.push(objectDesc)
		if category(varDesc) == "A" {
			if varDesc != objectDesc {
				g.code.u1(opCheckcast)
				g.code.u2(int(g.cp.classInfo(classOperand(varDesc))))
				g.pop(1)
				g.push(varDesc)
			}
		} else {
			w, _ := wrapperOf(string(varDesc))
			g.code.u1(opCheckcast)
			g.code.u2(int(g.cp.classInfo(string(w))))
			g.pop(1)
			g.push(descriptor("L" + string(w) + ";"))
			g.coerce(descOf(w), varDesc)
		}
		g.storeVar(varSlot, varDesc)
		names := g.takePending()
		g.breakTargets = append(g.breakTargets, branchTarget{label: endL, finallyDepth: len(g.finallyStack), names: names})
		g.continueTargets = append(g.continueTargets, branchTarget{label: startL, finallyDepth: len(g.finallyStack), names: names})
		g.inScope(func() bool { return g.emitStmt(s.Statement) })
		g.breakTargets = g.breakTargets[:len(g.breakTargets)-1]
		g.continueTargets = g.continueTargets[:len(g.continueTargets)-1]
		g.branchTo(opGoto, startL)
		g.placeLabel(endL)
		return false
	})
}

// emitTry emits the try statement (JLS 14.20), including try-with-resources.
func (g *bodyGen) emitTry(stmt *Node) bool {
	t := stmt.AsTryStatement()
	emitUserBody := func() bool { return g.inScope(func() bool { return g.emitStmt(t.TryBlock) }) }
	var fin *finallyAction
	if t.FinallyBlock != nil {
		fin = &finallyAction{kind: "block", block: t.FinallyBlock}
	}
	resources := arrayNodes(t.Resources)
	catchClauses := arrayNodes(t.CatchClauses)
	if len(resources) == 0 {
		return g.emitTryConstruct(emitUserBody, catchClauses, fin)
	}
	var emitResourceNest func(i int) bool
	emitResourceNest = func(i int) bool {
		if i >= len(resources) {
			return emitUserBody()
		}
		res := resources[i].AsResource()
		var desc descriptor
		var isInterface bool
		var valueExpr *Node
		switch {
		case res.Type != nil && res.Name != nil && res.Initializer != nil:
			desc = descriptorOf(res.Type, g.program, nil)
			var typeSymbol *Symbol
			if res.Type.Kind == TypeReference {
				typeSymbol = ResolveTypeEntityName(res.Type.AsTypeReference().TypeName, resources[i], g.program)
			}
			isInterface = typeSymbol != nil && typeSymbol.Flags&SymbolFlagsInterface != 0
			valueExpr = res.Initializer
		case res.Expression != nil:
			exprType := g.checker.GetTypeOfExpression(res.Expression)
			desc = typeToDescriptor(exprType, 0)
			isInterface = exprType.Kind == TypeKindClass && exprType.Symbol.Flags&SymbolFlagsInterface != 0
			valueExpr = res.Expression
		default:
			panic(unsupportedEmit{})
		}
		if desc[0] != 'L' {
			panic(unsupportedEmit{})
		}
		ownerInternal := internalName(desc[1 : len(desc)-1])
		s := g.reserveSlot(desc)
		if resources[i].Symbol != nil {
			g.locals[resources[i].Symbol] = localSlotInfo{slot: slot(s), descriptor: desc}
		}
		guarded := valueExpr.Kind != ObjectCreationExpression
		g.coerce(g.emitExpr(valueExpr), desc)
		g.storeVar(s, desc)
		action := &finallyAction{kind: "resource", slot: s, ownerInternal: ownerInternal, isInterface: isInterface, guarded: guarded}
		return g.emitTryConstruct(func() bool { return emitResourceNest(i + 1) }, nil, action)
	}
	return g.emitTryConstruct(func() bool { return emitResourceNest(0) }, catchClauses, fin)
}

func methodBodyNode(method *Node) *Node {
	switch method.Kind {
	case MethodDeclaration:
		return method.AsMethodDeclaration().Body
	case ConstructorDeclaration:
		return method.AsConstructorDeclaration().Body
	}
	return nil
}
