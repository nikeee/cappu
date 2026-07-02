package compiler

import "strings"

// topDesc is the sentinel pseudo-descriptor for an unassigned local slot.
const topDesc descriptor = " top"

type lineNumberEntry struct {
	pc   pc
	line int
}

// localVarEntry is one LocalVariableTable entry (JVMS 4.7.13).
type localVarEntry struct {
	startPc    pc
	length     int
	name       string
	descriptor descriptor
	slot       slot
}

// compiledMethod is the result of generateBody.
type compiledMethod struct {
	code           *byteBuffer
	maxStack       int
	maxLocals      int
	stackMapTable  *byteBuffer // nil when absent
	exceptionTable []exceptionTableEntry
	lineNumbers    []lineNumberEntry
	localVariables []localVarEntry
}

type frame struct {
	locals []descriptor
	stack  []descriptor
}

// label is a branch target, resolved when placed.
type label struct {
	offset              pc   // -1 until placed
	targeted            bool // some branch/switch entry jumps here (set by branchTo)
	targetStack         []descriptor
	hasTargetStack      bool
	assignedAtTarget    map[int]bool
	hasAssignedAtTarget bool
}

type fixup struct {
	at, from pc
	label    *label
}

type branchTarget struct {
	label        *label
	finallyDepth int
	names        []string
}

type yieldTarget struct {
	label *label
	desc  descriptor
}

type captureField struct {
	ownerInternal internalName
	fieldName     string
	descriptor    descriptor
}

type paramSym struct {
	symbol     *Symbol
	descriptor descriptor
}

type lambdaSpecT struct {
	params           []paramSym
	returnDescriptor descriptor
	body             *Node
	isInstance       bool
}

type ctorPrologueT struct {
	this0Descriptor descriptor
	hasThis0        bool
	captures        []localCapture
	superInternal   internalName
	superParamDescs []descriptor
}

type ctorLeadingT struct {
	this0Descriptor descriptor
	hasThis0        bool
	captures        []localCapture
}

type ctorTrailingStore struct {
	owner      internalName
	name       string
	descriptor descriptor
	slot       int
}

// bodyGenOptions are generateBody's optional parameters.
type bodyGenOptions struct {
	ctorSuper          internalName // "" if none
	hasCtorSuper       bool
	fieldInits         []fieldInit
	lambdaMethods      *[]*byteBuffer // sink for synthetic lambda methods
	lambdaSpec         *lambdaSpecT
	enumCtor           bool
	enumClinit         *enumClinit
	assertionsOwner    internalName // "" if none
	captureFields      map[*Symbol]captureField
	outerThis          internalName // "" if none (the enclosing internal name)
	ctorPrologue       *ctorPrologueT
	paramSymbols       []paramSym // nil if none
	ctorTrailingStores []ctorTrailingStore
	ctorLeading        *ctorLeadingT
}

// bodyGen holds the shared mutable state of one method-body generation. The TS
// closures over this state become methods.
type bodyGen struct {
	method           *Node
	cp               *constantPool
	program          *Program
	checker          *Checker
	thisInternalName internalName
	opts             bodyGenOptions

	isConstructor    bool
	isStatic         bool
	returnDescriptor descriptor
	enclosingName    string
	lambdaCounter    int

	locals       map[*Symbol]localSlotInfo
	activeLocals []localSlotInfo
	assigned     map[int]bool
	reachable    bool
	nextSlot     int
	maxLocals    int
	code         *byteBuffer

	lineNumbers   []lineNumberEntry
	lineSourceSet bool
	lineSource    *lineSourceInfo
	localVars     []localVarEntry

	frameAt         map[pc]frame
	fixups          []fixup
	wideFixups      []fixup
	exceptionTable  []exceptionTableEntry
	handlerOffsets  []pc
	breakTargets    []branchTarget
	continueTargets []branchTarget
	pendingLabels   []string
	yieldTargets    []yieldTarget
	finallyStack    []finallyAction
	stack           []descriptor
	maxStack        int
}

type lineSourceInfo struct {
	text   string
	starts []int
}

// newBodyGen sets up parameter/local slots and the body-generation state (the
// preamble of the TS generateBody before statement emission).
func newBodyGen(method *Node, cp *constantPool, program *Program, checker *Checker, thisInternalName internalName, opts bodyGenOptions) *bodyGen {
	g := &bodyGen{
		method:           method,
		cp:               cp,
		program:          program,
		checker:          checker,
		thisInternalName: thisInternalName,
		opts:             opts,
		locals:           map[*Symbol]localSlotInfo{},
		assigned:         map[int]bool{},
		reachable:        true,
		code:             &byteBuffer{},
		frameAt:          map[pc]frame{},
	}
	g.isConstructor = opts.lambdaSpec == nil && method.Kind == ConstructorDeclaration
	if opts.lambdaSpec != nil {
		g.isStatic = !opts.lambdaSpec.isInstance
	} else {
		g.isStatic = !g.isConstructor && methodAccessFlags(method)&accStatic != 0
	}
	switch {
	case opts.lambdaSpec != nil:
		g.returnDescriptor = opts.lambdaSpec.returnDescriptor
	case g.isConstructor:
		g.returnDescriptor = "V"
	default:
		g.returnDescriptor = descriptorOf(method.AsMethodDeclaration().ReturnType, program, nil)
	}
	switch {
	case opts.lambdaSpec != nil:
		g.enclosingName = "lambda"
	case g.isConstructor:
		g.enclosingName = "new"
	default:
		g.enclosingName = method.AsMethodDeclaration().Name.AsIdentifier().Text
	}

	g.nextSlot = 0
	if !g.isStatic {
		g.nextSlot = 1
		thisName := ""
		if emitDebugInfo {
			thisName = "this"
		}
		g.activeLocals = append(g.activeLocals, localSlotInfo{slot: 0, descriptor: descOf(thisInternalName), name: thisName, lvtStart: 0, hasLvtStart: true})
		g.assigned[0] = true
	}

	// Build the parameter list (lambda captures+own, record components, or the
	// method's parameters plus any synthetic leading parameters).
	type genParam struct {
		symbol     *Symbol
		descriptor descriptor
		hasSymbol  bool
	}
	var params []genParam
	switch {
	case opts.lambdaSpec != nil:
		for _, p := range opts.lambdaSpec.params {
			params = append(params, genParam{symbol: p.symbol, descriptor: p.descriptor, hasSymbol: p.symbol != nil})
		}
	case opts.paramSymbols != nil:
		for _, p := range opts.paramSymbols {
			params = append(params, genParam{symbol: p.symbol, descriptor: p.descriptor, hasSymbol: p.symbol != nil})
		}
	default:
		if opts.enumCtor {
			params = append(params, genParam{descriptor: stringDesc}, genParam{descriptor: "I"})
		}
		if opts.ctorPrologue != nil {
			if opts.ctorPrologue.hasThis0 {
				params = append(params, genParam{descriptor: opts.ctorPrologue.this0Descriptor})
			}
			for _, c := range opts.ctorPrologue.captures {
				params = append(params, genParam{descriptor: c.descriptor})
			}
			for _, d := range opts.ctorPrologue.superParamDescs {
				params = append(params, genParam{descriptor: d})
			}
		}
		if opts.ctorLeading != nil {
			if opts.ctorLeading.hasThis0 {
				params = append(params, genParam{descriptor: opts.ctorLeading.this0Descriptor})
			}
			for _, c := range opts.ctorLeading.captures {
				params = append(params, genParam{descriptor: c.descriptor})
			}
		}
		for _, p := range methodParameters(method) {
			params = append(params, genParam{symbol: p.Symbol, descriptor: paramDescriptor(p, program), hasSymbol: p.Symbol != nil})
		}
	}
	for _, p := range params {
		if p.hasSymbol {
			g.locals[p.symbol] = localSlotInfo{slot: slot(g.nextSlot), descriptor: p.descriptor}
		}
		name := ""
		if emitDebugInfo && p.hasSymbol {
			name = p.symbol.EscapedName
		}
		g.activeLocals = append(g.activeLocals, localSlotInfo{slot: slot(g.nextSlot), descriptor: p.descriptor, name: name, lvtStart: 0, hasLvtStart: true})
		g.assigned[g.nextSlot] = true
		g.nextSlot += slotsOf(p.descriptor)
	}
	g.maxLocals = g.nextSlot
	return g
}

// recordLine appends a LineNumberTable entry at the current pc for node's source line.
func (g *bodyGen) recordLine(node *Node) {
	if node.Pos < 0 || node.End <= node.Pos {
		return // synthetic
	}
	if !g.lineSourceSet {
		g.lineSourceSet = true
		p := node
		for p != nil && p.Kind != SourceFile {
			p = p.Parent
		}
		if p != nil {
			sf := p.AsSourceFile()
			g.lineSource = &lineSourceInfo{text: sf.Text, starts: sf.LineStarts()}
		}
	}
	if g.lineSource == nil {
		return
	}
	start := SkipTrivia(g.lineSource.text, node.Pos)
	if start >= len(g.lineSource.text) {
		return
	}
	line := GetLineAndCharacterOfPosition(g.lineSource.text, g.lineSource.starts, start).Line + 1 // 1-based
	if n := len(g.lineNumbers); n > 0 {
		last := &g.lineNumbers[n-1]
		if int(last.pc) == g.code.length() {
			last.line = line // previous entry emitted no code yet
			return
		}
		if last.line == line {
			return // the same line continues
		}
	}
	g.lineNumbers = append(g.lineNumbers, lineNumberEntry{pc: pc(g.code.length()), line: line})
}

// closeLocals collects LocalVariableTable entries for scopes closing at the current pc.
func (g *bodyGen) closeLocals(entries []localSlotInfo) {
	if !emitDebugInfo {
		return
	}
	end := g.code.length()
	for _, e := range entries {
		if e.name == "" || !e.hasLvtStart {
			continue
		}
		g.localVars = append(g.localVars, localVarEntry{
			startPc:    e.lvtStart,
			length:     end - int(e.lvtStart),
			name:       e.name,
			descriptor: e.descriptor,
			slot:       e.slot,
		})
	}
}

// inScope runs body in a nested local scope: locals it declares are dropped afterwards.
func (g *bodyGen) inScope(body func() bool) bool {
	savedActive := len(g.activeLocals)
	savedSlot := g.nextSlot
	terminated := body()
	g.closeLocals(g.activeLocals[savedActive:])
	g.activeLocals = g.activeLocals[:savedActive]
	for s := range g.assigned {
		if s >= savedSlot {
			delete(g.assigned, s)
		}
	}
	g.nextSlot = savedSlot
	return terminated
}

// frameLocals is the frame's locals: in-scope locals with their type, or top if
// the slot is not in assignedSet. Trailing tops are trimmed.
func (g *bodyGen) frameLocals(assignedSet map[int]bool) []descriptor {
	var out []descriptor
	for _, e := range g.activeLocals {
		if assignedSet[int(e.slot)] {
			out = append(out, e.descriptor)
		} else {
			out = append(out, topDesc)
		}
	}
	for len(out) > 0 && out[len(out)-1] == topDesc {
		out = out[:len(out)-1]
	}
	return out
}

func copyIntSet(s map[int]bool) map[int]bool {
	out := make(map[int]bool, len(s))
	for k := range s {
		out[k] = true
	}
	return out
}

func intersectIntSets(a, b map[int]bool) map[int]bool {
	out := map[int]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func (g *bodyGen) newLabel() *label { return &label{offset: -1} }

// placeLabel resolves a label at the current pc and records its stack-map frame.
func (g *bodyGen) placeLabel(l *label) {
	l.offset = pc(g.code.length())
	var here map[int]bool
	switch {
	case !l.hasAssignedAtTarget:
		here = copyIntSet(g.assigned)
	case g.reachable:
		here = intersectIntSets(l.assignedAtTarget, g.assigned)
	default:
		here = copyIntSet(l.assignedAtTarget)
	}
	g.assigned = here
	g.reachable = true
	var frameStack []descriptor
	if l.hasTargetStack {
		frameStack = append(frameStack, l.targetStack...)
	} else {
		frameStack = append(frameStack, g.stack...)
	}
	if l.hasTargetStack {
		g.stack = append(g.stack[:0:0], l.targetStack...)
	}
	g.frameAt[l.offset] = frame{locals: g.frameLocals(here), stack: frameStack}
}

// branchTo emits a branch op to a label, recording the target's frame.
func (g *bodyGen) branchTo(op int, l *label) {
	from := pc(g.code.length())
	g.code.u1(op)
	at := pc(g.code.length())
	g.code.u2(0) // placeholder, backpatched later
	g.fixups = append(g.fixups, fixup{at: at, from: from, label: l})
	l.targeted = true
	if !l.hasTargetStack {
		l.targetStack = append(l.targetStack[:0:0], g.stack...)
		l.hasTargetStack = true
	}
	if !l.hasAssignedAtTarget {
		l.assignedAtTarget = copyIntSet(g.assigned)
		l.hasAssignedAtTarget = true
	} else {
		l.assignedAtTarget = intersectIntSets(l.assignedAtTarget, g.assigned)
	}
	if op == opGoto {
		g.reachable = false
	}
}

// --- typed operand stack ----------------------------------------------------

func (g *bodyGen) push(d descriptor) {
	g.stack = append(g.stack, d)
	slots := 0
	for _, x := range g.stack {
		slots += slotsOf(x)
	}
	if slots > g.maxStack {
		g.maxStack = slots
	}
}

func (g *bodyGen) pushRef(d descriptor) { g.push(d) }

func (g *bodyGen) pop(count int) {
	for i := 0; i < count; i++ {
		if len(g.stack) > 0 {
			g.stack = g.stack[:len(g.stack)-1]
		}
	}
}

func (g *bodyGen) setStack(to []descriptor) {
	g.stack = append(g.stack[:0:0], to...)
}

// category is the numeric category of a descriptor: I, J, F, D, or A (reference).
func category(d descriptor) string {
	c := d[0]
	switch c {
	case 'J', 'D', 'F':
		return string(c)
	case 'L', '[':
		return "A"
	default:
		return "I"
	}
}

// box boxes a primitive on the stack to its wrapper: Xxx.valueOf.
func (g *bodyGen) box(prim string) {
	w, ok := wrapperOf(prim)
	if !ok {
		return
	}
	g.code.u1(opInvokestatic)
	g.code.u2(int(g.cp.methodref(string(w), "valueOf", methodDescriptor("("+prim+")L"+string(w)+";"))))
	g.pop(1)
	g.push(descriptor("L" + string(w) + ";"))
}

// unbox unboxes a wrapper reference on the stack to its primitive, returning that
// primitive's descriptor (or "" if from is not a wrapper).
func (g *bodyGen) unbox(from descriptor) descriptor {
	if from[0] != 'L' {
		return ""
	}
	wrapper := string(from[1 : len(from)-1])
	um, ok := unboxOf(wrapper)
	if !ok {
		return ""
	}
	g.code.u1(opInvokevirtual)
	g.code.u2(int(g.cp.methodref(wrapper, um.method, methodDescriptor("()"+string(um.prim)))))
	g.pop(1)
	g.push(um.prim)
	return um.prim
}

// coerce converts the value on top of the stack from `from` to `to` (JLS 5.1.2
// widening, 5.1.7 boxing, 5.1.8 unboxing).
func (g *bodyGen) coerce(from, to descriptor) {
	if from == to {
		return
	}
	a := category(from)
	b := category(to)
	if a != "A" && b == "A" {
		g.box(string(from))
		return
	}
	if a == "A" && b != "A" {
		prim := g.unbox(from)
		if prim != "" {
			g.coerce(prim, to)
		}
		return
	}
	if a == b {
		return
	}
	op := -1
	switch {
	case a == "I" && b == "J":
		op = opI2l
	case a == "I" && b == "F":
		op = opI2f
	case a == "I" && b == "D":
		op = opI2d
	case a == "J" && b == "F":
		op = opL2f
	case a == "J" && b == "D":
		op = opL2d
	case a == "F" && b == "D":
		op = opF2d
	}
	if op == -1 {
		return // narrowing / reference: nothing to insert here
	}
	g.code.u1(op)
	g.pop(1)
	g.push(descriptor(b)) // op defined => numeric, never "A"
}

func (g *bodyGen) ldc(index cpIndex) {
	if index <= 0xff {
		g.code.u1(opLdc)
		g.code.u1(int(index))
	} else {
		g.code.u1(opLdcW)
		g.code.u2(int(index))
	}
}

func (g *bodyGen) intConst(value int) {
	switch {
	case value >= -1 && value <= 5:
		g.code.u1(opIconstM1 + (value + 1))
	case value >= -128 && value <= 127:
		g.code.u1(opBipush)
		g.code.u1(value & 0xff)
	case value >= -32768 && value <= 32767:
		g.code.u1(opSipush)
		g.code.u2(value & 0xffff)
	default:
		g.ldc(g.cp.integer(value))
	}
}

func (g *bodyGen) longConst(value int64) {
	switch value {
	case 0:
		g.code.u1(opLconst0)
	case 1:
		g.code.u1(0x0a)
	default:
		g.code.u1(opLdc2W)
		g.code.u2(int(g.cp.long(value)))
	}
}

func (g *bodyGen) floatConst(value float64) {
	switch value {
	case 0:
		g.code.u1(opFconst0)
	case 1:
		g.code.u1(opFconst1)
	case 2:
		g.code.u1(opFconst2)
	default:
		g.ldc(g.cp.float(value))
	}
}

func (g *bodyGen) doubleConst(value float64) {
	switch value {
	case 0:
		g.code.u1(opDconst0)
	case 1:
		g.code.u1(opDconst1)
	default:
		g.code.u1(opLdc2W)
		g.code.u2(int(g.cp.double(value)))
	}
}

func (g *bodyGen) loadVar(varSlot int, d descriptor) {
	kind := category(d)
	full := map[string]int{"I": opIload, "J": opLload, "F": opFload, "D": opDload, "A": opAload}[kind]
	short0 := map[string]int{"I": opIload0, "J": opLload0, "F": opFload0, "D": opDload0, "A": opAloadBase0}[kind]
	if varSlot <= 3 {
		g.code.u1(short0 + varSlot)
	} else {
		g.code.u1(full)
		g.code.u1(varSlot)
	}
}

func (g *bodyGen) storeVar(varSlot int, d descriptor) {
	kind := category(d)
	full := map[string]int{"I": opIstore, "J": opLstore, "F": opFstore, "D": opDstore, "A": opAstore}[kind]
	short0 := map[string]int{"I": opIstore0, "J": opLstore0, "F": opFstore0, "D": opDstore0, "A": opAstore0}[kind]
	if varSlot <= 3 {
		g.code.u1(short0 + varSlot)
	} else {
		g.code.u1(full)
		g.code.u1(varSlot)
	}
	g.pop(1)
	g.assigned[varSlot] = true
}

// ctorArgInfo returns the argument types + descriptors for constructor-overload
// disambiguation.
func (g *bodyGen) ctorArgInfo(args []*Node) ([]descriptor, []*Type) {
	types := make([]*Type, len(args))
	descs := make([]descriptor, len(args))
	for i, a := range args {
		types[i] = g.checker.GetTypeOfExpression(a)
		descs[i] = typeToDescriptor(types[i], 0)
	}
	return descs, types
}

// fieldInfoOf resolves a field/enum-constant reference: where it lives and its descriptor.
func (g *bodyGen) fieldInfoOf(symbol *Symbol) fieldInfo {
	if symbol.Parent == nil {
		panic(unsupportedEmit{})
	}
	// An enum constant is a public static final field of the enum, typed as the enum.
	if symbol.Flags&SymbolFlagsEnumConstant != 0 {
		owner := binaryName(symbol.Parent)
		return fieldInfo{owner: owner, name: symbol.EscapedName, descriptor: descOf(owner), isStatic: true}
	}
	// A record component is a private final instance field of the record.
	if symbol.ValueDeclaration != nil && symbol.ValueDeclaration.Kind == RecordComponent {
		return fieldInfo{
			owner:      binaryName(symbol.Parent),
			name:       symbol.EscapedName,
			descriptor: descriptorOf(symbol.ValueDeclaration.AsRecordComponent().Type, g.program, nil),
			isStatic:   false,
		}
	}
	declarator := symbol.ValueDeclaration
	if declarator == nil || declarator.Kind != VariableDeclarator {
		panic(unsupportedEmit{})
	}
	field := declarator.Parent
	if field.Kind != FieldDeclaration {
		panic(unsupportedEmit{})
	}
	// A field declared in an interface is implicitly public static final (JLS 9.3).
	inInterface := symbol.Parent.Flags&SymbolFlagsInterface != 0
	return fieldInfo{
		owner:      binaryName(symbol.Parent),
		name:       symbol.EscapedName,
		descriptor: withRank(descriptorOf(field.AsFieldDeclaration().Type, g.program, nil), declarator.AsVariableDeclarator().ArrayRankAfterName),
		isStatic:   isStaticDeclaration(field) || inInterface,
	}
}

// implicitRefOwner is the fieldref owner for an implicit-this access: javac
// references an INHERITED instance field through the current class.
func (g *bodyGen) implicitRefOwner(info fieldInfo) internalName {
	if !info.isStatic &&
		info.owner != g.thisInternalName &&
		(g.opts.outerThis == "" || info.owner != g.opts.outerThis) &&
		!strings.HasPrefix(string(g.thisInternalName), string(info.owner)+"$") {
		return g.thisInternalName
	}
	return info.owner
}

// emitFieldRead reads a field: getstatic, or emit the receiver then getfield.
func (g *bodyGen) emitFieldRead(info fieldInfo, emitReceiver func()) descriptor {
	if info.isStatic {
		g.code.u1(opGetstatic)
	} else {
		emitReceiver()
		g.code.u1(opGetfield)
		g.pop(1) // the receiver, replaced by the field value
	}
	g.code.u2(int(g.cp.fieldref(info.owner, info.name, info.descriptor)))
	g.push(info.descriptor)
	return info.descriptor
}

// erasedCheckcast inserts a synthetic checkcast after reading an erased generic
// member (JLS 5.2), when the access's instantiated type is more specific.
func (g *bodyGen) erasedCheckcast(node *Node, d descriptor) descriptor {
	if d[0] != 'L' && d[0] != '[' {
		return d
	}
	actual := g.checker.GetTypeOfExpression(node)
	actualDesc := typeToDescriptor(actual, 0)
	if actualDesc != d && actualDesc != objectDesc && (actual.Kind == TypeKindClass || actual.Kind == TypeKindArray) {
		g.code.u1(opCheckcast)
		g.code.u2(int(g.cp.classInfo(classOperand(actualDesc))))
		g.pop(1)
		g.push(actualDesc)
		return actualDesc
	}
	return d
}

// emitImplicitReceiver emits the receiver for an implicit-`this` member access:
// `this`, or - for a local/anonymous class reaching an enclosing-instance member
// - `this.this$0`.
func (g *bodyGen) emitImplicitReceiver(ownerInternal internalName) {
	if g.opts.outerThis != "" && ownerInternal == g.opts.outerThis {
		g.code.u1(opAload0)
		g.pushRef(descOf(g.thisInternalName))
		g.code.u1(opGetfield)
		g.code.u2(int(g.cp.fieldref(g.thisInternalName, "this$0", descOf(g.opts.outerThis))))
		g.pop(1)
		g.pushRef(descOf(g.opts.outerThis))
		return
	}
	// A member of an enclosing class with no this$0 route: degrade.
	if ownerInternal != g.thisInternalName && strings.HasPrefix(string(g.thisInternalName), string(ownerInternal)+"$") {
		panic(unsupportedEmit{})
	}
	g.code.u1(opAload0)
	g.pushRef(objectDesc)
}
