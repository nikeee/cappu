package compiler

import (
	"sort"
	"strings"
)

// JVM class-file writer and bytecode code generation. Implements the JVM
// Specification (JVMS SE 21): chapter 4 (the class file format, constant pool,
// fields/methods, Code and StackMapTable attributes) and chapter 6 (the
// instruction set). The entry point is emitClass(declaration) -> one .class file.
// emitter.go drives this per source file and is where higher-level, source-level
// logic (e.g. constant folding) belongs. We target major version 65 (Java 21).
// Port of src/compiler/bytecode.ts.
//
// Reference output is cross-checked against `javac` in the tests.

// Notified whenever a body cannot be fully compiled and degrades to a verifiable
// placeholder. The compiler driver surfaces these as warnings; unset, degradation
// stays silent.
var degradeListener func(className, member string)

// SetDegradeListener registers a callback invoked when a member degrades.
func SetDegradeListener(listener func(className, member string)) {
	degradeListener = listener
}

// When set, method bodies emit a LocalVariableTable (JVMS 4.7.13) like `javac
// -g`; off (the default) matches default-flags javac. Scoped to one
// emitSourceFile run by the emitter (set/reset around the emit loop).
var emitDebugInfo = false

// SetEmitDebugInfo toggles LocalVariableTable emission and returns the previous value.
func SetEmitDebugInfo(on bool) bool {
	previous := emitDebugInfo
	emitDebugInfo = on
	return previous
}

// unsupportedEmit is thrown (via panic) when a construct is not yet handled by
// code generation; the caller falls back to a verifiable placeholder body so
// output is always valid.
type unsupportedEmit struct{}

const (
	classMagic   = 0xcafebabe
	minorVersion = 0
	majorVersion = 65 // Java 21
)

// Access flags (JVMS 4.1 / 4.5 / 4.6). accPublic/accProtected/accStatic/accFinal/
// accBridge/accVarargs/accInterface/accAbstract/accSynthetic/accEnum are shared
// with classfile_reader.go; the rest are emitter-only.
const (
	accPrivate      = 0x0002
	accSuper        = 0x0020
	accVolatile     = 0x0040
	accTransient    = 0x0080
	accSynchronized = 0x0020
	accNative       = 0x0100
	accStrict       = 0x0800
)

// The three string domains of the class-file format, branded so an internal name
// cannot land where a descriptor belongs (and vice versa).

// descriptor is a field/return type descriptor (JVMS 4.3.2): a primitive, `L<internal>;`,
// or `[<descriptor>`. Primitive descriptors ("B","C",...,"V") are plain descriptors.
type descriptor string

// methodDescriptor is a method descriptor (JVMS 4.3.3), e.g. "(ILjava/lang/String;)V".
type methodDescriptor string

// internalName is an internal (slash-separated) binary class name (JVMS 4.2.1).
type internalName string

// jvmSignature is a generic signature (JVMS 4.7.9): a JavaTypeSignature,
// MethodSignature or ClassSignature.
type jvmSignature string

// pc is a bytecode offset into a method's Code array.
type pc int

// slot is a local-variable slot index (JVMS 2.6.1; long/double occupy two).
type slot int

// Primitive field descriptors (JVMS 4.3.2) by source keyword.
var primitiveDescriptorByKeyword = map[string]descriptor{
	"byte": "B", "char": "C", "double": "D", "float": "F",
	"int": "I", "long": "J", "short": "S", "boolean": "Z", "void": "V",
}

// primitiveDescriptor returns the primitive descriptor for a source keyword, ok=false otherwise.
func primitiveDescriptor(keyword string) (descriptor, bool) {
	d, ok := primitiveDescriptorByKeyword[keyword]
	return d, ok
}

const (
	objectDesc    descriptor = "Ljava/lang/Object;"
	stringDesc    descriptor = "Ljava/lang/String;"
	throwableDesc descriptor = "Ljava/lang/Throwable;"
	iteratorDesc  descriptor = "Ljava/util/Iterator;"
)

// withRank prepends `rank` array dimensions (C-style brackets, JLS 10.2).
func withRank(d descriptor, rank int) descriptor {
	if rank > 0 {
		return descriptor(strings.Repeat("[", rank) + string(d))
	}
	return d
}

// descOf returns the object descriptor `L<name>;` for an internal name.
func descOf(internal internalName) descriptor {
	return descriptor("L" + string(internal) + ";")
}

// classOperand returns the CONSTANT_Class operand for a reference type given by
// its descriptor: array classes are named by the descriptor itself, others by
// the internal name inside `L...;` (JVMS 4.4.1).
func classOperand(d descriptor) string {
	if len(d) > 0 && d[0] == '[' {
		return string(d)
	}
	return string(d[1 : len(d)-1])
}

// Constant pool tags (JVMS 4.4, Table 4.4-A).
const (
	constantUtf8               = 1
	constantInteger            = 3
	constantFloat              = 4
	constantLong               = 5
	constantDouble             = 6
	constantClass              = 7
	constantString             = 8
	constantFieldref           = 9
	constantMethodref          = 10
	constantInterfaceMethodref = 11
	constantNameAndType        = 12
	constantMethodHandle       = 15
	constantMethodType         = 16
	constantInvokeDynamic      = 18
)

// MethodHandle reference_kind (JVMS 4.4.8).
const (
	refInvokeVirtual    = 5
	refInvokeStatic     = 6
	refInvokeSpecial    = 7
	refNewInvokeSpecial = 8
	refInvokeInterface  = 9
)

// Bootstrap-method descriptors and names (each typed explicitly).
const makeConcat = "makeConcatWithConstants"
const opInvokeDynamic = 0xba

// java.lang.invoke.StringConcatFactory.makeConcatWithConstants bootstrap (JLS 15.18.1).
const stringConcatFactory internalName = "java/lang/invoke/StringConcatFactory"
const makeConcatBsmDescriptor methodDescriptor = "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/MethodType;Ljava/lang/String;[Ljava/lang/Object;)Ljava/lang/invoke/CallSite;"

// java.lang.invoke.LambdaMetafactory.metafactory bootstrap (JLS 15.27 lambdas).
const lambdaMetafactory internalName = "java/lang/invoke/LambdaMetafactory"
const lambdaMetafactoryBsmDescriptor methodDescriptor = "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/MethodType;Ljava/lang/invoke/MethodType;Ljava/lang/invoke/MethodHandle;Ljava/lang/invoke/MethodType;)Ljava/lang/invoke/CallSite;"

// java.lang.runtime.ObjectMethods.bootstrap (record equals/hashCode/toString).
const objectMethodsBsmDescriptor methodDescriptor = "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/TypeDescriptor;Ljava/lang/Class;Ljava/lang/String;[Ljava/lang/invoke/MethodHandle;)Ljava/lang/Object;"

// Boxing/unboxing (JLS 5.1.7/5.1.8): primitive descriptor -> wrapper internal
// name (Xxx.valueOf), and wrapper internal name -> [unboxing method, primitive].
var wrapperByPrim = map[string]internalName{
	"Z": "java/lang/Boolean", "B": "java/lang/Byte", "S": "java/lang/Short",
	"C": "java/lang/Character", "I": "java/lang/Integer", "J": "java/lang/Long",
	"F": "java/lang/Float", "D": "java/lang/Double",
}

// wrapperOf returns the wrapper class for a primitive descriptor (JLS 5.1.7), ok=false otherwise.
func wrapperOf(prim string) (internalName, bool) {
	w, ok := wrapperByPrim[prim]
	return w, ok
}

type unboxInfo struct {
	method string
	prim   descriptor
}

var unboxByWrapper = map[string]unboxInfo{
	"java/lang/Boolean":   {"booleanValue", "Z"},
	"java/lang/Byte":      {"byteValue", "B"},
	"java/lang/Short":     {"shortValue", "S"},
	"java/lang/Character": {"charValue", "C"},
	"java/lang/Integer":   {"intValue", "I"},
	"java/lang/Long":      {"longValue", "J"},
	"java/lang/Float":     {"floatValue", "F"},
	"java/lang/Double":    {"doubleValue", "D"},
}

// unboxOf returns the unboxing method and its primitive for a wrapper's internal
// name (JLS 5.1.8), ok=false otherwise.
func unboxOf(internal string) (unboxInfo, bool) {
	u, ok := unboxByWrapper[internal]
	return u, ok
}

// Opcodes (JVMS 6.5).
const (
	opAconstNull      = 0x01
	opIconst0         = 0x03
	opIconst1         = 0x04
	opLconst0         = 0x09
	opFconst0         = 0x0b
	opFconst1         = 0x0c
	opFconst2         = 0x0d
	opDconst0         = 0x0e
	opDconst1         = 0x0f
	opLcmp            = 0x94
	opFcmpl           = 0x95
	opFcmpg           = 0x96
	opDcmpl           = 0x97
	opDcmpg           = 0x98
	opIreturn         = 0xac
	opLreturn         = 0xad
	opFreturn         = 0xae
	opDreturn         = 0xaf
	opAreturn         = 0xb0
	opIconstM1        = 0x02
	opBipush          = 0x10
	opSipush          = 0x11
	opLdc             = 0x12
	opLdcW            = 0x13
	opLdc2W           = 0x14
	opIload           = 0x15
	opLload           = 0x16
	opFload           = 0x17
	opDload           = 0x18
	opAload           = 0x19
	opIload0          = 0x1a
	opLload0          = 0x1e
	opFload0          = 0x22
	opDload0          = 0x26
	opAloadBase0      = 0x2a // aload_0
	opIstore          = 0x36
	opLstore          = 0x37
	opFstore          = 0x38
	opDstore          = 0x39
	opAstore          = 0x3a
	opIstore0         = 0x3b
	opLstore0         = 0x3f
	opFstore0         = 0x43
	opDstore0         = 0x47
	opAstore0         = 0x4b
	opIadd            = 0x60 // arithmetic bases; + type offset (I=0,J=1,F=2,D=3)
	opIsub            = 0x64
	opImul            = 0x68
	opIdiv            = 0x6c
	opIrem            = 0x70
	opIneg            = 0x74 // negate base; + type offset
	opIshl            = 0x78 // shift bases; + (long ? 1 : 0)
	opIshr            = 0x7a
	opIushr           = 0x7c
	opIand            = 0x7e
	opIor             = 0x80
	opIxor            = 0x82
	opLxor            = 0x83
	opI2l             = 0x85
	opI2f             = 0x86
	opI2d             = 0x87
	opL2i             = 0x88
	opL2f             = 0x89
	opL2d             = 0x8a
	opF2i             = 0x8b
	opF2l             = 0x8c
	opF2d             = 0x8d
	opD2i             = 0x8e
	opD2l             = 0x8f
	opD2f             = 0x90
	opI2b             = 0x91
	opI2c             = 0x92
	opI2s             = 0x93
	opCheckcast       = 0xc0
	opInstanceof      = 0xc1
	opPop             = 0x57
	opPop2            = 0x58
	opDup             = 0x59
	opDup2            = 0x5c
	opNew             = 0xbb
	opNewarray        = 0xbc
	opAnewarray       = 0xbd
	opArraylength     = 0xbe
	opAthrow          = 0xbf
	opMonitorenter    = 0xc2
	opMonitorexit     = 0xc3
	opMultianewarray  = 0xc5
	opIaload          = 0x2e
	opIastore         = 0x4f
	opAastore         = 0x53
	opGetstatic       = 0xb2
	opPutstatic       = 0xb3
	opGetfield        = 0xb4
	opPutfield        = 0xb5
	opInvokevirtual   = 0xb6
	opInvokespecial   = 0xb7
	opInvokestatic    = 0xb8
	opInvokeinterface = 0xb9
	opIinc            = 0x84
	opIfeq            = 0x99 // if<cond> against 0; +offset within (eq,ne,lt,ge,gt,le)
	opIfIcmpeq        = 0x9f // if_icmp<cond>; same offset order
	opIfAcmpeq        = 0xa5
	opIfAcmpne        = 0xa6
	opGoto            = 0xa7
	opTableswitch     = 0xaa
	opLookupswitch    = 0xab
	opIfnull          = 0xc6
	opIfnonnull       = 0xc7
	opAload0          = 0x2a
	opReturn          = 0xb1
)

// StackMapTable verification_type_info tags (JVMS 4.7.4).
const (
	itemTop     = 0
	itemInteger = 1
	itemFloat   = 2
	itemDouble  = 3
	itemLong    = 4
	itemObject  = 7
	fullFrame   = 255
)

// generateBody generates real bytecode for a method body (the TS generateBody).
func generateBody(method *Node, cp *constantPool, program *Program, checker *Checker, thisInternalName internalName, opts bodyGenOptions) compiledMethod {
	g := newBodyGen(method, cp, program, checker, thisInternalName, opts)

	if opts.lambdaSpec != nil {
		ls := opts.lambdaSpec
		switch {
		case ls.body.Kind == Block:
			terminated := g.emitStmt(ls.body)
			if !terminated {
				if g.returnDescriptor == "V" {
					g.code.u1(opReturn)
				} else {
					panic(unsupportedEmit{})
				}
			}
		case g.returnDescriptor == "V":
			g.emitStatementExpression(ls.body)
			g.code.u1(opReturn)
		default:
			g.coerce(g.emitExpr(ls.body), g.returnDescriptor)
			g.emitReturn()
		}
	} else {
		body := methodBodyNode(method)
		if body == nil || body.Kind != Block {
			panic(unsupportedEmit{})
		}
		stmts := blockStatements(body)
		var firstStmt *Node
		if len(stmts) > 0 {
			firstStmt = stmts[0]
		}
		var leadingCall *Node
		if g.isConstructor && !opts.enumCtor && firstStmt != nil && firstStmt.Kind == ExpressionStatement &&
			firstStmt.AsExpressionStatement().Expression.Kind == CallExpression {
			leadingCall = firstStmt.AsExpressionStatement().Expression
		}
		var explicitInvocation *Node
		if leadingCall != nil {
			ce := leadingCall.AsCallExpression().Expression
			if ce.Kind == SuperExpression || ce.Kind == ThisExpression {
				explicitInvocation = leadingCall
			}
		}
		isThisCall := explicitInvocation != nil && explicitInvocation.AsCallExpression().Expression.Kind == ThisExpression

		leadSlot := 1
		leadThis0Slot := -1
		if opts.ctorLeading != nil && opts.ctorLeading.hasThis0 {
			leadThis0Slot = leadSlot
			leadSlot++
		}
		var leadCaptureSlots []int
		if opts.ctorLeading != nil {
			for _, c := range opts.ctorLeading.captures {
				leadCaptureSlots = append(leadCaptureSlots, leadSlot)
				leadSlot += slotsOf(c.descriptor)
			}
		}
		if leadThis0Slot >= 0 && !isThisCall {
			g.code.u1(opAload0)
			g.pushRef(descOf(thisInternalName))
			g.loadVar(leadThis0Slot, opts.ctorLeading.this0Descriptor)
			g.push(opts.ctorLeading.this0Descriptor)
			g.code.u1(opPutfield)
			g.code.u2(int(g.cp.fieldref(thisInternalName, "this$0", opts.ctorLeading.this0Descriptor)))
			g.pop(2)
		}
		switch {
		case g.isConstructor && opts.enumCtor:
			g.code.u1(opAload0)
			g.pushRef(objectDesc)
			g.code.u1(opAload0 + 1) // aload_1 (name)
			g.pushRef(stringDesc)
			g.code.u1(opIload0 + 2) // iload_2 (ordinal)
			g.push("I")
			g.code.u1(opInvokespecial)
			g.code.u2(int(g.cp.methodref("java/lang/Enum", "<init>", "(Ljava/lang/String;I)V")))
			g.pop(3)
		case g.isConstructor && explicitInvocation != nil:
			classDecl := method.Parent
			var targetSymbol *Symbol
			if isThisCall {
				if classDecl != nil {
					targetSymbol = classDecl.Symbol
				}
			} else if classDecl != nil && classDecl.Kind == ClassDeclaration && classDecl.AsClassDeclaration().ExtendsType != nil &&
				classDecl.AsClassDeclaration().ExtendsType.Kind == TypeReference {
				targetSymbol = ResolveTypeEntityName(classDecl.AsClassDeclaration().ExtendsType.AsTypeReference().TypeName, classDecl, program)
			}
			owner := opts.ctorSuper
			if isThisCall {
				owner = thisInternalName
			}
			args := arrayNodes(explicitInvocation.AsCallExpression().Arguments)
			var target *Node
			if targetSymbol != nil {
				descs, types := g.ctorArgInfo(args)
				target = findConstructor(targetSymbol, len(args), program, descs, &findCtorRefs{checker: checker, argTypes: types})
			}
			if owner == "" || target == nil {
				panic(unsupportedEmit{})
			}
			paramDescs := ctorParamDescs(target, program)
			var leadDescs []descriptor
			if isThisCall && opts.ctorLeading != nil {
				if opts.ctorLeading.hasThis0 {
					leadDescs = append(leadDescs, opts.ctorLeading.this0Descriptor)
				}
				for _, c := range opts.ctorLeading.captures {
					leadDescs = append(leadDescs, c.descriptor)
				}
			}
			g.code.u1(opAload0)
			g.pushRef(objectDesc)
			if isThisCall && opts.ctorLeading != nil {
				if leadThis0Slot >= 0 {
					g.loadVar(leadThis0Slot, opts.ctorLeading.this0Descriptor)
					g.push(opts.ctorLeading.this0Descriptor)
				}
				for i, c := range opts.ctorLeading.captures {
					g.loadVar(leadCaptureSlots[i], c.descriptor)
					g.push(c.descriptor)
				}
			}
			for i, arg := range args {
				g.coerce(g.emitExpr(arg), paramDescs[i])
			}
			g.code.u1(opInvokespecial)
			g.code.u2(int(g.cp.methodref(string(owner), "<init>", methodDescriptor("("+joinDescs(leadDescs, paramDescs)+")V"))))
			g.pop(1 + len(leadDescs) + len(args))
		case g.isConstructor && opts.ctorPrologue != nil:
			cpro := opts.ctorPrologue
			s := 1
			this0Slot := -1
			if cpro.hasThis0 {
				this0Slot = s
				s++
			}
			var captureSlots []int
			for _, c := range cpro.captures {
				captureSlots = append(captureSlots, s)
				s += slotsOf(c.descriptor)
			}
			var superSlots []int
			for _, d := range cpro.superParamDescs {
				superSlots = append(superSlots, s)
				s += slotsOf(d)
			}
			if this0Slot >= 0 {
				g.code.u1(opAload0)
				g.pushRef(objectDesc)
				g.loadVar(this0Slot, cpro.this0Descriptor)
				g.pushRef(cpro.this0Descriptor)
				g.code.u1(opPutfield)
				g.code.u2(int(g.cp.fieldref(thisInternalName, "this$0", cpro.this0Descriptor)))
				g.pop(2)
			}
			g.code.u1(opAload0)
			g.pushRef(objectDesc)
			for i, d := range cpro.superParamDescs {
				g.loadVar(superSlots[i], d)
				g.push(d)
			}
			g.code.u1(opInvokespecial)
			g.code.u2(int(g.cp.methodref(string(cpro.superInternal), "<init>", methodDescriptor("("+joinDescs(cpro.superParamDescs)+")V"))))
			g.pop(1 + len(cpro.superParamDescs))
			for i, c := range cpro.captures {
				g.code.u1(opAload0)
				g.pushRef(objectDesc)
				g.loadVar(captureSlots[i], c.descriptor)
				g.push(c.descriptor)
				g.code.u1(opPutfield)
				g.code.u2(int(g.cp.fieldref(thisInternalName, c.fieldName, c.descriptor)))
				g.pop(2)
			}
		case g.isConstructor && opts.ctorSuper != "":
			g.code.u1(opAload0)
			g.pushRef(objectDesc)
			g.code.u1(opInvokespecial)
			g.code.u2(int(g.cp.methodref(string(opts.ctorSuper), "<init>", "()V")))
			g.pop(1)
		}
		if !isThisCall && opts.ctorLeading != nil {
			for i, c := range opts.ctorLeading.captures {
				g.code.u1(opAload0)
				g.pushRef(descOf(thisInternalName))
				g.loadVar(leadCaptureSlots[i], c.descriptor)
				g.push(c.descriptor)
				g.code.u1(opPutfield)
				g.code.u2(int(g.cp.fieldref(thisInternalName, c.fieldName, c.descriptor)))
				g.pop(2)
			}
		}
		if opts.enumClinit != nil {
			g.emitEnumClinitPrologue(opts.enumClinit)
		}
		if opts.assertionsOwner != "" {
			g.ldc(g.cp.classInfo(string(opts.assertionsOwner)))
			g.push("Ljava/lang/Class;")
			g.code.u1(opInvokevirtual)
			g.code.u2(int(g.cp.methodref("java/lang/Class", "desiredAssertionStatus", "()Z")))
			g.pop(1)
			g.push("I")
			enabledL := g.newLabel()
			storeL := g.newLabel()
			g.pop(1)
			g.branchTo(opIfeq+1, enabledL)
			g.intConst(1)
			g.push("I")
			g.branchTo(opGoto, storeL)
			g.pop(1)
			g.placeLabel(enabledL)
			g.intConst(0)
			g.push("I")
			g.placeLabel(storeL)
			g.code.u1(opPutstatic)
			g.code.u2(int(g.cp.fieldref(opts.assertionsOwner, "$assertionsDisabled", "Z")))
			g.pop(1)
		}
		if !isThisCall {
			for _, fi := range opts.fieldInits {
				switch {
				case fi.block != nil:
					g.inScope(func() bool {
						for _, st := range blockStatements(fi.block) {
							g.emitStmt(st)
						}
						return false
					})
				case fi.isStatic:
					g.coerce(g.emitExpr(fi.init), fi.descriptor)
					g.code.u1(opPutstatic)
					g.code.u2(int(g.cp.fieldref(fi.owner, fi.name, fi.descriptor)))
					g.pop(1)
				default:
					g.code.u1(opAload0)
					g.pushRef(objectDesc)
					g.coerce(g.emitExpr(fi.init), fi.descriptor)
					g.code.u1(opPutfield)
					g.code.u2(int(g.cp.fieldref(fi.owner, fi.name, fi.descriptor)))
					g.pop(2)
				}
			}
		}
		var terminated bool
		if explicitInvocation != nil {
			terminated = g.inScope(func() bool {
				t := false
				for i := 1; i < len(stmts); i++ {
					t = g.emitStmt(stmts[i])
				}
				return t
			})
		} else {
			terminated = g.emitStmt(body)
		}
		if opts.ctorTrailingStores != nil {
			if terminated {
				panic(unsupportedEmit{})
			}
			for _, st := range opts.ctorTrailingStores {
				g.code.u1(opAload0)
				g.pushRef(objectDesc)
				g.loadVar(st.slot, st.descriptor)
				g.push(st.descriptor)
				g.code.u1(opPutfield)
				g.code.u2(int(g.cp.fieldref(st.owner, st.name, st.descriptor)))
				g.pop(2)
			}
		}
		if !terminated {
			if g.returnDescriptor == "V" {
				g.code.u1(opReturn)
			} else {
				panic(unsupportedEmit{})
			}
		}
	}

	// Backpatch branch offsets (signed, relative to the branch opcode address).
	for _, f := range g.fixups {
		if f.label.offset < 0 {
			panic(unsupportedEmit{})
		}
		g.code.patchU2(int(f.at), int(f.label.offset-f.from)&0xffff)
	}
	for _, f := range g.wideFixups {
		if f.label.offset < 0 {
			panic(unsupportedEmit{})
		}
		g.code.patchU4(int(f.at), int(f.label.offset-f.from)&0xffffffff)
	}

	// StackMapTable: a full_frame at every branch-target offset (JVMS 4.7.4).
	offsetSet := map[pc]bool{}
	var targetOffsets []pc
	addOffset := func(o pc) {
		if !offsetSet[o] {
			offsetSet[o] = true
			targetOffsets = append(targetOffsets, o)
		}
	}
	for _, f := range g.fixups {
		addOffset(f.label.offset)
	}
	for _, f := range g.wideFixups {
		addOffset(f.label.offset)
	}
	for _, o := range g.handlerOffsets {
		addOffset(o)
	}
	sort.Slice(targetOffsets, func(a, b int) bool { return targetOffsets[a] < targetOffsets[b] })
	var stackMapTable *byteBuffer
	if len(targetOffsets) > 0 {
		writeVerification := func(buf *byteBuffer, d descriptor) {
			if d == topDesc {
				buf.u1(itemTop)
				return
			}
			switch category(d) {
			case "I":
				buf.u1(itemInteger)
			case "F":
				buf.u1(itemFloat)
			case "D":
				buf.u1(itemDouble)
			case "J":
				buf.u1(itemLong)
			default:
				buf.u1(itemObject)
				buf.u2(int(g.cp.classInfo(classOperand(d))))
			}
		}
		stackMapTable = &byteBuffer{}
		stackMapTable.u2(len(targetOffsets))
		previous := pc(-1)
		for _, offset := range targetOffsets {
			fr := g.frameAt[offset]
			stackMapTable.u1(fullFrame)
			if previous < 0 {
				stackMapTable.u2(int(offset))
			} else {
				stackMapTable.u2(int(offset - previous - 1))
			}
			stackMapTable.u2(len(fr.locals))
			for _, d := range fr.locals {
				writeVerification(stackMapTable, d)
			}
			stackMapTable.u2(len(fr.stack))
			for _, d := range fr.stack {
				writeVerification(stackMapTable, d)
			}
			previous = offset
		}
	}

	g.closeLocals(g.activeLocals)
	var localVariables []localVarEntry
	if emitDebugInfo {
		localVariables = g.localVars
		sort.SliceStable(localVariables, func(a, b int) bool {
			ea := int(localVariables[a].startPc) + localVariables[a].length
			eb := int(localVariables[b].startPc) + localVariables[b].length
			if ea != eb {
				return ea < eb
			}
			return localVariables[a].slot < localVariables[b].slot
		})
	}

	return compiledMethod{
		code:           g.code,
		maxStack:       g.maxStack,
		maxLocals:      g.maxLocals,
		stackMapTable:  stackMapTable,
		exceptionTable: g.exceptionTable,
		lineNumbers:    g.lineNumbers,
		localVariables: localVariables,
	}
}

// emitMethod emits one method_info, degrading to a verifiable placeholder body
// for any construct generateBody cannot yet handle.
func emitMethod(method *Node, cp *constantPool, program *Program, checker *Checker, thisInternalName internalName, lambdaMethods *[]*byteBuffer, extraFlags int, captureFields map[*Symbol]captureField, outerThis internalName) *byteBuffer {
	flags := methodAccessFlags(method) | extraFlags
	desc := methodDescriptorOf(method, program)
	signature, hasSignature := methodSignatureOf(method, program)

	info := &byteBuffer{}
	info.u2(flags)
	info.u2(int(cp.utf8(method.AsMethodDeclaration().Name.AsIdentifier().Text)))
	info.u2(int(cp.utf8(string(desc))))

	// Method + parameter annotations (after Code/Signature, javac's order).
	annBuf, annCount := methodAnnotationAttributes(cp, method.AsMethodDeclaration(), method, program)

	if flags&(accAbstract|accNative) != 0 || method.AsMethodDeclaration().Body == nil {
		nAttr := annCount
		if hasSignature {
			nAttr++
		}
		info.u2(nAttr)
		if hasSignature {
			writeSignatureAttribute(info, cp, signature)
		}
		info.appendBuf(annBuf)
		return info
	}

	var body compiledMethod
	degraded := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(unsupportedEmit); !ok {
					panic(r)
				}
				degraded = true
			}
		}()
		body = generateBody(method, cp, program, checker, thisInternalName, bodyGenOptions{lambdaMethods: lambdaMethods, captureFields: captureFields, outerThis: outerThis})
	}()
	if degraded {
		if degradeListener != nil {
			degradeListener(string(thisInternalName), method.AsMethodDeclaration().Name.AsIdentifier().Text)
		}
		isStatic := flags&accStatic != 0
		argsSize := 0
		if !isStatic {
			argsSize = 1
		}
		for _, p := range arrayNodes(method.AsMethodDeclaration().Parameters) {
			argsSize += slotsOf(paramDescriptor(p, program))
		}
		fcode, fmax := defaultReturnBody(returnDescriptorOf(desc))
		body = compiledMethod{code: fcode, maxStack: fmax, maxLocals: argsSize}
	}

	writeCodeAttribute(info, cp, body, signature, hasSignature, annBuf, annCount)
	return info
}

// emitLambdaMethod emits a synthetic method holding a lambda body.
func emitLambdaMethod(impl lambdaImpl, cp *constantPool, program *Program, checker *Checker, thisInternalName internalName, lambdaMethods *[]*byteBuffer) *byteBuffer {
	var ps []descriptor
	for _, p := range impl.params {
		ps = append(ps, p.descriptor)
	}
	desc := methodDescriptor("(" + joinDescs(ps) + ")" + string(impl.returnDescriptor))
	info := &byteBuffer{}
	flags := accPrivate | accSynthetic
	if !impl.isInstance {
		flags |= accStatic
	}
	info.u2(flags)
	info.u2(int(cp.utf8(impl.name)))
	info.u2(int(cp.utf8(string(desc))))
	body := generateBody(nil, cp, program, checker, thisInternalName, bodyGenOptions{
		lambdaMethods: lambdaMethods,
		lambdaSpec:    &lambdaSpecT{params: impl.params, returnDescriptor: impl.returnDescriptor, body: impl.body, isInstance: impl.isInstance},
	})
	writeCodeAttribute(info, cp, body, "", false, nil, 0)
	return info
}

// emitArrayCtorRefMethod emits the synthetic impl for `T[]::new` (JLS 15.13.3).
func emitArrayCtorRefMethod(cp *constantPool, name string, arrayDesc descriptor) *byteBuffer {
	elem := arrayDesc[1:]
	code := &byteBuffer{}
	code.u1(opIload0)
	if atype, ok := newarrayAtypeMap[elem]; ok {
		code.u1(opNewarray)
		code.u1(atype)
	} else {
		code.u1(opAnewarray)
		code.u2(int(cp.classInfo(classOperand(elem))))
	}
	code.u1(opAreturn)
	info := &byteBuffer{}
	info.u2(accPrivate | accStatic | accSynthetic)
	info.u2(int(cp.utf8(name)))
	info.u2(int(cp.utf8("(I)" + string(arrayDesc))))
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 1, maxLocals: 1}, "", false, nil, 0)
	return info
}

// writeCodeAttribute appends the Code attribute (with optional StackMapTable) and,
// when generic, a Signature attribute (JVMS 4.7.9).
func writeCodeAttribute(info *byteBuffer, cp *constantPool, body compiledMethod, signature jvmSignature, hasSignature bool, extra *byteBuffer, extraCount int) {
	smt := body.stackMapTable
	smtBytes := 0
	if smt != nil {
		smtBytes = 6 + smt.length()
	}
	lines := body.lineNumbers
	lntBytes := 0
	if len(lines) > 0 {
		lntBytes = 6 + 2 + 4*len(lines)
	}
	locals := body.localVariables
	lvtBytes := 0
	if len(locals) > 0 {
		lvtBytes = 6 + 2 + 10*len(locals)
	}
	handlers := body.exceptionTable

	codeAttr := &byteBuffer{}
	codeAttr.u2(int(cp.utf8("Code")))
	codeAttr.u4(12 + body.code.length() + len(handlers)*8 + lntBytes + lvtBytes + smtBytes)
	codeAttr.u2(body.maxStack)
	codeAttr.u2(body.maxLocals)
	codeAttr.u4(body.code.length())
	codeAttr.appendBuf(body.code)
	codeAttr.u2(len(handlers))
	for _, h := range handlers {
		codeAttr.u2(int(h.start))
		codeAttr.u2(int(h.end))
		codeAttr.u2(int(h.handler))
		codeAttr.u2(int(h.catchType))
	}
	nAttr := 0
	if len(lines) > 0 {
		nAttr++
	}
	if len(locals) > 0 {
		nAttr++
	}
	if smt != nil {
		nAttr++
	}
	codeAttr.u2(nAttr)
	if len(lines) > 0 {
		codeAttr.u2(int(cp.utf8("LineNumberTable")))
		codeAttr.u4(2 + 4*len(lines))
		codeAttr.u2(len(lines))
		for _, l := range lines {
			codeAttr.u2(int(l.pc))
			codeAttr.u2(l.line)
		}
	}
	if len(locals) > 0 {
		codeAttr.u2(int(cp.utf8("LocalVariableTable")))
		codeAttr.u4(2 + 10*len(locals))
		codeAttr.u2(len(locals))
		for _, v := range locals {
			codeAttr.u2(int(v.startPc))
			codeAttr.u2(v.length)
			codeAttr.u2(int(cp.utf8(v.name)))
			codeAttr.u2(int(cp.utf8(string(v.descriptor))))
			codeAttr.u2(int(v.slot))
		}
	}
	if smt != nil {
		codeAttr.u2(int(cp.utf8("StackMapTable")))
		codeAttr.u4(smt.length())
		codeAttr.appendBuf(smt)
	}

	methodAttrs := 1 + extraCount
	if hasSignature {
		methodAttrs++
	}
	info.u2(methodAttrs)
	info.appendBuf(codeAttr)
	if hasSignature {
		writeSignatureAttribute(info, cp, signature)
	}
	if extra != nil {
		info.appendBuf(extra)
	}
}

// ctorMethodLeading carries the synthetic leading parameters of a declared
// constructor of a member inner / capturing local class.
type ctorMethodLeading struct {
	this0Descriptor descriptor
	hasThis0        bool
	captures        []localCapture
	outerThis       internalName
}

// emitConstructorMethod emits a declared constructor's method_info.
func emitConstructorMethod(ctor *Node, flags int, cp *constantPool, program *Program, checker *Checker, thisInternalName, superInternalName internalName, instanceInits []fieldInit, lambdaMethods *[]*byteBuffer, leading *ctorMethodLeading) *byteBuffer {
	userParams := ""
	for _, p := range arrayNodes(ctor.AsConstructorDeclaration().Parameters) {
		userParams += string(paramDescriptor(p, program))
	}
	leadParams := ""
	if leading != nil {
		if leading.hasThis0 {
			leadParams += string(leading.this0Descriptor)
		}
		for _, c := range leading.captures {
			leadParams += string(c.descriptor)
		}
	}
	desc := methodDescriptor("(" + leadParams + userParams + ")V")
	info := &byteBuffer{}
	info.u2(flags)
	info.u2(int(cp.utf8("<init>")))
	info.u2(int(cp.utf8(string(desc))))

	captureFields := map[*Symbol]captureField{}
	if leading != nil {
		for _, c := range leading.captures {
			captureFields[c.symbol] = captureField{ownerInternal: thisInternalName, fieldName: c.fieldName, descriptor: c.descriptor}
		}
	}
	var ctorLeading *ctorLeadingT
	outerThis := internalName("")
	if leading != nil {
		ctorLeading = &ctorLeadingT{this0Descriptor: leading.this0Descriptor, hasThis0: leading.hasThis0, captures: leading.captures}
		outerThis = leading.outerThis
	}
	var body compiledMethod
	degraded := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(unsupportedEmit); !ok {
					panic(r)
				}
				degraded = true
			}
		}()
		body = generateBody(ctor, cp, program, checker, thisInternalName, bodyGenOptions{
			ctorSuper: superInternalName, hasCtorSuper: true, fieldInits: instanceInits,
			lambdaMethods: lambdaMethods, captureFields: captureFields, outerThis: outerThis, ctorLeading: ctorLeading,
		})
	}()
	if degraded {
		if degradeListener != nil {
			degradeListener(string(thisInternalName), "<init>")
		}
		leadSlots := 0
		if leading != nil {
			if leading.hasThis0 {
				leadSlots++
			}
			for _, c := range leading.captures {
				leadSlots += slotsOf(c.descriptor)
			}
		}
		argsSize := 1 + leadSlots
		for _, p := range arrayNodes(ctor.AsConstructorDeclaration().Parameters) {
			argsSize += slotsOf(paramDescriptor(p, program))
		}
		code := &byteBuffer{}
		code.u1(opAload0)
		code.u1(opInvokespecial)
		code.u2(int(cp.methodref(string(superInternalName), "<init>", "()V")))
		code.u1(opReturn)
		body = compiledMethod{code: code, maxStack: 1, maxLocals: argsSize}
	}

	signature, hasSignature := methodSignatureOf(ctor, program)
	if leading != nil {
		hasSignature = false // spliced parameters: the signature no longer matches
	}
	writeCodeAttribute(info, cp, body, signature, hasSignature, nil, 0)
	return info
}

// EmittedClass is one compiled .class file.
type EmittedClass struct {
	Name          string // internal/binary name, e.g. "com/app/Foo"
	Bytes         []byte
	HasMainMethod bool
}

// resolveInternalName resolves a type reference to its internal name, falling back
// to its written (dotted -> slashed) form.
func resolveInternalName(typeNode, from *Node, program *Program) internalName {
	if typeNode == nil || typeNode.Kind != TypeReference {
		return ""
	}
	ref := typeNode.AsTypeReference()
	symbol := ResolveTypeEntityName(ref.TypeName, from, program)
	if symbol != nil {
		return binaryName(symbol)
	}
	return internalName(strings.ReplaceAll(entityNameToString(ref.TypeName), ".", "/"))
}

// classUsesAssert reports whether the class's own code uses `assert` (JLS 14.10).
func classUsesAssert(declaration *Node) bool {
	found := false
	var visit func(node *Node)
	visit = func(node *Node) {
		if found {
			return
		}
		if node.Kind == AssertStatement {
			found = true
			return
		}
		if node != declaration && isTypeDeclarationKind(node.Kind) {
			return
		}
		node.ForEachChild(func(child *Node) bool {
			visit(child)
			return false
		})
	}
	visit(declaration)
	return found
}

// loadByDescriptor emits the load instruction for a value of descriptor d in slot.
func loadByDescriptor(code *byteBuffer, d descriptor, sl int) {
	var op int
	switch d[0] {
	case 'J':
		op = opLload
	case 'D':
		op = opDload
	case 'F':
		op = opFload
	case 'L', '[':
		op = opAload
	default:
		op = opIload
	}
	if sl <= 3 {
		code.u1(opIload0 + (op-opIload)*4 + sl)
	} else {
		code.u1(op)
		code.u1(sl)
	}
}

// emitSynthCtor emits the synthesized constructor for a capturing local/anonymous class.
func emitSynthCtor(cp *constantPool, name, superInternal internalName, superParamDescs []descriptor, captures []localCapture, this0Descriptor, superThis0Descriptor descriptor, accessFlags int) *byteBuffer {
	code := &byteBuffer{}
	sl := 1
	superThis0Slot := 0
	if superThis0Descriptor != "" {
		superThis0Slot = sl
		sl++
	}
	this0Slot := 0
	if this0Descriptor != "" {
		this0Slot = sl
		sl++
	}
	var captureSlots []int
	for _, c := range captures {
		captureSlots = append(captureSlots, sl)
		sl += slotsOf(c.descriptor)
	}
	var superSlots []int
	for _, d := range superParamDescs {
		superSlots = append(superSlots, sl)
		sl += slotsOf(d)
	}
	maxStack := 1
	if this0Descriptor != "" {
		code.u1(opAload0)
		code.u1(opAload)
		code.u1(this0Slot)
		code.u1(opPutfield)
		code.u2(int(cp.fieldref(name, "this$0", this0Descriptor)))
		if maxStack < 2 {
			maxStack = 2
		}
	}
	code.u1(opAload0)
	if superThis0Descriptor != "" {
		loadByDescriptor(code, superThis0Descriptor, superThis0Slot)
		code.u1(opDup)
		code.u1(opInvokestatic)
		code.u2(int(cp.methodref("java/util/Objects", "requireNonNull", "(Ljava/lang/Object;)Ljava/lang/Object;")))
		code.u1(opPop)
	}
	for i, d := range superParamDescs {
		loadByDescriptor(code, d, superSlots[i])
	}
	var superDescs []descriptor
	if superThis0Descriptor != "" {
		superDescs = append(superDescs, superThis0Descriptor)
	}
	superDescs = append(superDescs, superParamDescs...)
	code.u1(opInvokespecial)
	code.u2(int(cp.methodref(string(superInternal), "<init>", methodDescriptor("("+joinDescs(superDescs)+")V"))))
	superStack := 1
	if superThis0Descriptor != "" {
		superStack += 2
	}
	for _, d := range superParamDescs {
		superStack += slotsOf(d)
	}
	if superStack > maxStack {
		maxStack = superStack
	}
	for i, c := range captures {
		code.u1(opAload0)
		loadByDescriptor(code, c.descriptor, captureSlots[i])
		code.u1(opPutfield)
		code.u2(int(cp.fieldref(name, c.fieldName, c.descriptor)))
		if 1+slotsOf(c.descriptor) > maxStack {
			maxStack = 1 + slotsOf(c.descriptor)
		}
	}
	code.u1(opReturn)
	info := &byteBuffer{}
	info.u2(accessFlags) // package-private (anon) or ACC_PRIVATE (enum body)
	info.u2(int(cp.utf8("<init>")))
	var descs []descriptor
	if superThis0Descriptor != "" {
		descs = append(descs, superThis0Descriptor)
	}
	if this0Descriptor != "" {
		descs = append(descs, this0Descriptor)
	}
	for _, c := range captures {
		descs = append(descs, c.descriptor)
	}
	descs = append(descs, superParamDescs...)
	info.u2(int(cp.utf8("(" + joinDescs(descs) + ")V")))
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: maxStack, maxLocals: sl}, "", false, nil, 0)
	return info
}

// emitSynthCtorWithInits is like emitSynthCtor but runs the class's instance field
// initializers after the prologue.
func emitSynthCtorWithInits(cp *constantPool, name internalName, program *Program, checker *Checker, prologue ctorPrologueT, instanceInits []fieldInit, lambdaMethods *[]*byteBuffer) *byteBuffer {
	f := &NodeFactory{}
	synthCtor := f.NewConstructorDeclaration(nil, nil, nil, &NodeArray{}, nil, f.NewBlock(&NodeArray{}))
	captureFields := map[*Symbol]captureField{}
	for _, c := range prologue.captures {
		captureFields[c.symbol] = captureField{ownerInternal: name, fieldName: c.fieldName, descriptor: c.descriptor}
	}
	outerThis := internalName("")
	if prologue.hasThis0 {
		outerThis = internalName(classOperand(prologue.this0Descriptor))
	}
	var body compiledMethod
	degraded := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(unsupportedEmit); !ok {
					panic(r)
				}
				degraded = true
			}
		}()
		pro := prologue
		body = generateBody(synthCtor, cp, program, checker, name, bodyGenOptions{
			fieldInits: instanceInits, lambdaMethods: lambdaMethods, captureFields: captureFields, outerThis: outerThis, ctorPrologue: &pro,
		})
	}()
	if degraded {
		if degradeListener != nil {
			degradeListener(string(name), "<init>")
		}
		return emitSynthCtor(cp, name, prologue.superInternal, prologue.superParamDescs, prologue.captures, prologue.this0Descriptor, "", 0)
	}
	info := &byteBuffer{}
	info.u2(0)
	info.u2(int(cp.utf8("<init>")))
	var descs []descriptor
	if prologue.hasThis0 {
		descs = append(descs, prologue.this0Descriptor)
	}
	for _, c := range prologue.captures {
		descs = append(descs, c.descriptor)
	}
	descs = append(descs, prologue.superParamDescs...)
	info.u2(int(cp.utf8("(" + joinDescs(descs) + ")V")))
	writeCodeAttribute(info, cp, body, "", false, nil, 0)
	return info
}

// newClinitNode builds a synthetic `static void <clinit>()` method node.
func newClinitNode() *Node {
	f := &NodeFactory{}
	return f.NewMethodDeclaration(
		&NodeArray{Nodes: []*Node{f.newToken(StaticKeyword)}}, nil,
		f.NewPrimitiveType(VoidKeyword), f.NewIdentifier("<clinit>"),
		&NodeArray{}, nil, f.NewBlock(&NodeArray{}), nil)
}

func newDefaultCtorNode() *Node {
	f := &NodeFactory{}
	return f.NewConstructorDeclaration(nil, nil, nil, &NodeArray{}, nil, f.NewBlock(&NodeArray{}))
}

// emitClass emits a user-declared class (JLS 8) -> one .class file.
func emitClass(declaration *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) EmittedClass {
	program.GetGlobalIndex()
	d := declaration.AsClassDeclaration()
	var name internalName
	if declaration.Symbol != nil {
		name = binaryName(declaration.Symbol)
	} else {
		name = internalName(d.Name.AsIdentifier().Text)
	}
	superInternalName := resolveInternalName(d.ExtendsType, declaration, program)
	if superInternalName == "" {
		superInternalName = "java/lang/Object"
	}
	var interfaceNames []internalName
	for _, t := range arrayNodes(d.ImplementsTypes) {
		if n := resolveInternalName(t, declaration, program); n != "" {
			interfaceNames = append(interfaceNames, n)
		}
	}

	accessFlags := classAccessFlags(declaration)
	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo(string(superInternalName))
	var interfaceIndices []cpIndex
	for _, n := range interfaceNames {
		interfaceIndices = append(interfaceIndices, cp.classInfo(string(n)))
	}
	fieldsBuf, fieldCount := emitFields(declaration, cp, program)

	usesAssert := classUsesAssert(declaration)
	if usesAssert {
		fieldsBuf.u2(accStatic | accFinal | accSynthetic)
		fieldsBuf.u2(int(cp.utf8("$assertionsDisabled")))
		fieldsBuf.u2(int(cp.utf8("Z")))
		fieldsBuf.u2(0)
		fieldCount++
	}

	localCaptures := effectiveLocalCaptures(declaration, program, checker)
	outerThis := localOuterThis(declaration, program, checker)
	if outerThis == "" {
		outerThis = memberInnerThis0(declaration, program, checker)
	}
	this0Descriptor := descriptor("")
	if outerThis != "" {
		this0Descriptor = descOf(outerThis)
		emitFieldInfo(fieldsBuf, cp, accFinal|accSynthetic, "this$0", this0Descriptor)
		fieldCount++
	}
	for _, c := range localCaptures {
		emitFieldInfo(fieldsBuf, cp, accFinal|accSynthetic, c.fieldName, c.descriptor)
		fieldCount++
	}
	captureFieldMap := map[*Symbol]captureField{}
	for _, c := range localCaptures {
		captureFieldMap[c.symbol] = captureField{ownerInternal: name, fieldName: c.fieldName, descriptor: c.descriptor}
	}

	instanceInits, staticInits := collectFieldInits(arrayNodes(d.Members), name, program)

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer
	var declaredConstructors []*Node
	for _, m := range arrayNodes(d.Members) {
		if m.Kind == ConstructorDeclaration {
			declaredConstructors = append(declaredConstructors, m)
		}
	}
	hasThis0 := this0Descriptor != ""
	switch {
	case (len(localCaptures) > 0 || hasThis0) && len(declaredConstructors) == 0:
		prologue := ctorPrologueT{this0Descriptor: this0Descriptor, hasThis0: hasThis0, captures: localCaptures, superInternal: superInternalName, superParamDescs: nil}
		if len(instanceInits) > 0 {
			methods.appendBuf(emitSynthCtorWithInits(cp, name, program, checker, prologue, instanceInits, &lambdaMethods))
		} else {
			methods.appendBuf(emitSynthCtor(cp, name, superInternalName, nil, localCaptures, this0Descriptor, "", 0))
		}
		methodCount++
	case (hasThis0 || len(localCaptures) > 0) && len(declaredConstructors) > 0:
		for _, ctor := range declaredConstructors {
			methods.appendBuf(emitConstructorMethod(ctor, methodAccessFlags(ctor), cp, program, checker, name, superInternalName, instanceInits, &lambdaMethods,
				&ctorMethodLeading{this0Descriptor: this0Descriptor, hasThis0: hasThis0, captures: localCaptures, outerThis: outerThis}))
			methodCount++
		}
	case len(declaredConstructors) == 0:
		flags := accessFlags & (accPublic | accProtected | accPrivate)
		methods.appendBuf(emitConstructorMethod(newDefaultCtorNode(), flags, cp, program, checker, name, superInternalName, instanceInits, &lambdaMethods, nil))
		methodCount++
	default:
		for _, ctor := range declaredConstructors {
			methods.appendBuf(emitConstructorMethod(ctor, methodAccessFlags(ctor), cp, program, checker, name, superInternalName, instanceInits, &lambdaMethods, nil))
			methodCount++
		}
	}
	for _, member := range arrayNodes(d.Members) {
		if member.Kind != MethodDeclaration {
			continue
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, 0, captureFieldMap, outerThis))
		methodCount++
	}

	if len(staticInits) > 0 || usesAssert {
		info := &byteBuffer{}
		info.u2(accStatic)
		info.u2(int(cp.utf8("<clinit>")))
		info.u2(int(cp.utf8("()V")))
		assertOwner := internalName("")
		if usesAssert {
			assertOwner = name
		}
		var clinitBody compiledMethod
		degraded := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(unsupportedEmit); !ok {
						panic(r)
					}
					degraded = true
				}
			}()
			clinitBody = generateBody(newClinitNode(), cp, program, checker, name, bodyGenOptions{fieldInits: staticInits, lambdaMethods: &lambdaMethods, assertionsOwner: assertOwner})
		}()
		if degraded {
			if degradeListener != nil {
				degradeListener(string(name), "<clinit>")
			}
			code := &byteBuffer{}
			code.u1(opReturn)
			clinitBody = compiledMethod{code: code, maxStack: 0, maxLocals: 0}
		}
		writeCodeAttribute(info, cp, clinitBody, "", false, nil, 0)
		methods.appendBuf(info)
		methodCount++
	}

	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	sig, hasSig := classSignatureOf(declaration, program)
	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, sig, hasSig, innerClasses, "", permittedSubclassesOf(declaration.AsClassDeclaration().PermitsTypes, declaration, program), &annotationSource{modifiers: declaration.AsClassDeclaration().Modifiers, from: declaration, program: program})

	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fieldsBuf, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
	}
}

// emitFieldInfo writes a field_info with no attributes.
func emitFieldInfo(buffer *byteBuffer, cp *constantPool, flags int, name string, desc descriptor) {
	buffer.u2(flags)
	buffer.u2(int(cp.utf8(name)))
	buffer.u2(int(cp.utf8(string(desc))))
	buffer.u2(0)
}

// emitInterface emits a user-declared interface (JLS 9).
func emitInterface(declaration *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) EmittedClass {
	program.GetGlobalIndex()
	di := declaration.AsInterfaceDeclaration()
	var name internalName
	if declaration.Symbol != nil {
		name = binaryName(declaration.Symbol)
	} else {
		name = internalName(di.Name.AsIdentifier().Text)
	}
	var interfaceNames []internalName
	for _, t := range arrayNodes(di.ExtendsTypes) {
		if n := resolveInternalName(t, declaration, program); n != "" {
			interfaceNames = append(interfaceNames, n)
		}
	}
	accessFlags := accInterface | accAbstract
	if hasModifierKind(di.Modifiers, PublicKeyword) {
		accessFlags |= accPublic
	}

	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo("java/lang/Object")
	var interfaceIndices []cpIndex
	for _, n := range interfaceNames {
		interfaceIndices = append(interfaceIndices, cp.classInfo(string(n)))
	}

	fields := &byteBuffer{}
	fieldCount := 0
	for _, member := range arrayNodes(di.Members) {
		if member.Kind != FieldDeclaration {
			continue
		}
		field := member.AsFieldDeclaration()
		desc := descriptorOf(field.Type, program, nil)
		for _, declarator := range arrayNodes(field.Declarators) {
			dv := declarator.AsVariableDeclarator()
			init := dv.Initializer
			constIndex := cpIndex(0)
			hasConst := false
			if init != nil {
				if desc == stringDesc && init.Kind == StringLiteral {
					constIndex = cp.stringConst(init.AsLiteralExpression().Value)
					hasConst = true
				} else if folded := FoldConstant(init); folded != nil {
					switch desc {
					case "J", "Z", "I", "S", "B", "C":
						var intValue int64
						if folded.Kind == ConstBool {
							if folded.Bool {
								intValue = 1
							}
						} else {
							intValue = folded.Int
						}
						if desc == "J" {
							constIndex = cp.long(intValue)
						} else {
							constIndex = cp.integer(int(int32(intValue)))
						}
						hasConst = true
					}
				}
			}
			fields.u2(accPublic | accStatic | accFinal)
			fields.u2(int(cp.utf8(dv.Name.AsIdentifier().Text)))
			fields.u2(int(cp.utf8(string(desc))))
			if !hasConst {
				fields.u2(0)
			} else {
				fields.u2(1)
				fields.u2(int(cp.utf8("ConstantValue")))
				fields.u4(2)
				fields.u2(int(constIndex))
			}
			fieldCount++
		}
	}

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer
	for _, member := range arrayNodes(di.Members) {
		if member.Kind != MethodDeclaration {
			continue
		}
		m := member.AsMethodDeclaration()
		isPrivate := hasModifierKind(m.Modifiers, PrivateKeyword)
		extra := 0
		if !isPrivate {
			extra |= accPublic
		}
		if m.Body == nil {
			extra |= accAbstract
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, extra, nil, ""))
		methodCount++
	}
	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, "", false, innerClasses, "", permittedSubclassesOf(declaration.AsInterfaceDeclaration().PermitsTypes, declaration, program), &annotationSource{modifiers: declaration.AsInterfaceDeclaration().Modifiers, from: declaration, program: program})
	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fields, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
	}
}

// emitAnonymousClassIfPossible emits an anonymous class, or ok=false if unsupported.
func emitAnonymousClassIfPossible(node *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) (EmittedClass, bool) {
	target := anonymousTarget(node, program)
	if target == nil {
		return EmittedClass{}, false
	}
	name := anonymousClassName(node, program)
	classBody := arrayNodes(node.AsObjectCreationExpression().ClassBody)
	captures := collectCaptures(classBody, node.Pos, node.End, program, checker)

	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo(string(target.superInternal))
	var interfaceIndices []cpIndex
	if target.interfaceInternal != "" {
		interfaceIndices = []cpIndex{cp.classInfo(string(target.interfaceInternal))}
	}

	outerThis := outerThisInfo(classBody, node.Parent, program, checker)
	this0Descriptor := descriptor("")
	if outerThis != "" {
		this0Descriptor = descOf(outerThis)
	}

	if target.superThis0Desc != "" && (len(captures) > 0 || this0Descriptor != "" || !superTakesThis0(target, program, checker)) {
		return EmittedClass{}, false
	}

	fields := &byteBuffer{}
	fieldCount := 0
	if this0Descriptor != "" {
		emitFieldInfo(fields, cp, accFinal|accSynthetic, "this$0", this0Descriptor)
		fieldCount++
	}
	for _, c := range captures {
		emitFieldInfo(fields, cp, accFinal|accSynthetic, c.fieldName, c.descriptor)
		fieldCount++
	}
	declaredFieldsBuf, declaredFieldsCount := emitFieldsFromMembers(classBody, cp, program)
	fields.appendBuf(declaredFieldsBuf)
	fieldCount += declaredFieldsCount
	instanceInits, _ := collectFieldInits(classBody, name, program)

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer
	if target.superThis0Desc != "" && len(instanceInits) > 0 {
		return EmittedClass{}, false
	}
	if len(instanceInits) > 0 {
		methods.appendBuf(emitSynthCtorWithInits(cp, name, program, checker, ctorPrologueT{
			this0Descriptor: this0Descriptor, hasThis0: this0Descriptor != "", captures: captures,
			superInternal: target.superInternal, superParamDescs: target.superParamDescs,
		}, instanceInits, &lambdaMethods))
	} else {
		methods.appendBuf(emitSynthCtor(cp, name, target.superInternal, target.superParamDescs, captures, this0Descriptor, target.superThis0Desc, 0))
	}
	methodCount++

	captureMap := map[*Symbol]captureField{}
	for _, c := range captures {
		captureMap[c.symbol] = captureField{ownerInternal: name, fieldName: c.fieldName, descriptor: c.descriptor}
	}
	for _, member := range classBody {
		if member.Kind != FieldDeclaration {
			continue
		}
		field := member.AsFieldDeclaration()
		if isStaticDeclaration(member) {
			continue
		}
		desc := descriptorOf(field.Type, program, nil)
		for _, d := range arrayNodes(field.Declarators) {
			if d.Symbol != nil {
				captureMap[d.Symbol] = captureField{ownerInternal: name, fieldName: d.AsVariableDeclarator().Name.AsIdentifier().Text, descriptor: desc}
			}
		}
	}
	for _, member := range classBody {
		if member.Kind != MethodDeclaration {
			continue
		}
		extra := 0
		if target.interfaceInternal != "" {
			extra = accPublic
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, extra, captureMap, outerThis))
		methodCount++
	}
	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	attrs := buildClassAttributes(cp, sourceNameOf(node), name, nestMembers, "", false, innerClasses, "", nil, nil)
	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accSuper, thisClassIndex, superClassIndex, interfaceIndices, fields, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
	}, true
}

// emitValuesMethod emits `public static E[] values() { return (E[]) $VALUES.clone(); }`.
func emitValuesMethod(cp *constantPool, name internalName, valuesField string, arrayDesc descriptor) *byteBuffer {
	info := &byteBuffer{}
	info.u2(accPublic | accStatic)
	info.u2(int(cp.utf8("values")))
	info.u2(int(cp.utf8("()" + string(arrayDesc))))
	code := &byteBuffer{}
	code.u1(opGetstatic)
	code.u2(int(cp.fieldref(name, valuesField, arrayDesc)))
	code.u1(opInvokevirtual)
	code.u2(int(cp.methodref(string(arrayDesc), "clone", "()Ljava/lang/Object;")))
	code.u1(opCheckcast)
	code.u2(int(cp.classInfo(string(arrayDesc))))
	code.u1(opAreturn)
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 1, maxLocals: 0}, "", false, nil, 0)
	return info
}

// emitValueOfMethod emits `public static E valueOf(String name)`.
func emitValueOfMethod(cp *constantPool, name internalName, selfDesc descriptor) *byteBuffer {
	info := &byteBuffer{}
	info.u2(accPublic | accStatic)
	info.u2(int(cp.utf8("valueOf")))
	info.u2(int(cp.utf8("(Ljava/lang/String;)" + string(selfDesc))))
	code := &byteBuffer{}
	code.u1(opLdcW)
	code.u2(int(cp.classInfo(string(name))))
	code.u1(opAload0)
	code.u1(opInvokestatic)
	code.u2(int(cp.methodref("java/lang/Enum", "valueOf", "(Ljava/lang/Class;Ljava/lang/String;)Ljava/lang/Enum;")))
	code.u1(opCheckcast)
	code.u2(int(cp.classInfo(string(name))))
	code.u1(opAreturn)
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 2, maxLocals: 1}, "", false, nil, 0)
	return info
}

// emitEnumConstructor emits an enum constructor in enumCtor mode.
func emitEnumConstructor(ctor *Node, cp *constantPool, program *Program, checker *Checker, name internalName, instanceInits []fieldInit, lambdaMethods *[]*byteBuffer) *byteBuffer {
	userParams := ""
	for _, p := range arrayNodes(ctor.AsConstructorDeclaration().Parameters) {
		userParams += string(paramDescriptor(p, program))
	}
	desc := methodDescriptor("(Ljava/lang/String;I" + userParams + ")V")
	info := &byteBuffer{}
	info.u2(accPrivate)
	info.u2(int(cp.utf8("<init>")))
	info.u2(int(cp.utf8(string(desc))))
	var body compiledMethod
	degraded := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(unsupportedEmit); !ok {
					panic(r)
				}
				degraded = true
			}
		}()
		body = generateBody(ctor, cp, program, checker, name, bodyGenOptions{ctorSuper: "java/lang/Enum", hasCtorSuper: true, fieldInits: instanceInits, lambdaMethods: lambdaMethods, enumCtor: true})
	}()
	if degraded {
		if degradeListener != nil {
			degradeListener(string(name), "<init>")
		}
		argsSize := 3
		for _, p := range arrayNodes(ctor.AsConstructorDeclaration().Parameters) {
			argsSize += slotsOf(paramDescriptor(p, program))
		}
		code := &byteBuffer{}
		code.u1(opAload0)
		code.u1(opAload0 + 1)
		code.u1(opIload0 + 2)
		code.u1(opInvokespecial)
		code.u2(int(cp.methodref("java/lang/Enum", "<init>", "(Ljava/lang/String;I)V")))
		code.u1(opReturn)
		body = compiledMethod{code: code, maxStack: 3, maxLocals: argsSize}
	}
	writeCodeAttribute(info, cp, body, "", false, nil, 0)
	return info
}

func returnOp(d descriptor) int {
	switch d[0] {
	case 'J':
		return opLreturn
	case 'D':
		return opDreturn
	case 'F':
		return opFreturn
	case 'L', '[':
		return opAreturn
	case 'V':
		return opReturn
	default:
		return opIreturn
	}
}

// tryGenerateBody runs generateBody, returning ok=false on an unsupportedEmit panic.
func tryGenerateBody(method *Node, cp *constantPool, program *Program, checker *Checker, thisInternalName internalName, opts bodyGenOptions) (cm compiledMethod, ok bool) {
	ok = true
	defer func() {
		if r := recover(); r != nil {
			if _, isU := r.(unsupportedEmit); !isU {
				panic(r)
			}
			ok = false
		}
	}()
	cm = generateBody(method, cp, program, checker, thisInternalName, opts)
	return
}

// emitRecord emits a record declaration (JLS 8.10).
func emitRecord(declaration *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) EmittedClass {
	program.GetGlobalIndex()
	dr := declaration.AsRecordDeclaration()
	var name internalName
	if declaration.Symbol != nil {
		name = binaryName(declaration.Symbol)
	} else {
		name = internalName(dr.Name.AsIdentifier().Text)
	}
	isPublic := hasModifierKind(dr.Modifiers, PublicKeyword)
	accessFlags := accSuper | accFinal
	if isPublic {
		accessFlags |= accPublic
	}
	var interfaceNames []internalName
	for _, t := range arrayNodes(dr.ImplementsTypes) {
		if n := resolveInternalName(t, declaration, program); n != "" {
			interfaceNames = append(interfaceNames, n)
		}
	}
	componentNodes := arrayNodes(dr.RecordComponents)
	components := make([]recordComponent, len(componentNodes))
	for i, c := range componentNodes {
		components[i] = recordComponent{name: c.AsRecordComponent().Name.AsIdentifier().Text, descriptor: descriptorOf(c.AsRecordComponent().Type, program, nil)}
	}

	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo("java/lang/Record")
	var interfaceIndices []cpIndex
	for _, n := range interfaceNames {
		interfaceIndices = append(interfaceIndices, cp.classInfo(string(n)))
	}

	fields := &byteBuffer{}
	fieldCount := 0
	for _, c := range components {
		emitFieldInfo(fields, cp, accPrivate|accFinal, c.name, c.descriptor)
		fieldCount++
	}
	declaredFieldsBuf, declaredFieldsCount := emitFields(declaration, cp, program)
	fields.appendBuf(declaredFieldsBuf)
	fieldCount += declaredFieldsCount

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer

	var componentDescs []descriptor
	for _, c := range components {
		componentDescs = append(componentDescs, c.descriptor)
	}
	ctorDescriptor := methodDescriptor("(" + joinDescs(componentDescs) + ")V")
	emitImplicitCanonicalCtor := func() *byteBuffer {
		code := &byteBuffer{}
		code.u1(opAload0)
		code.u1(opInvokespecial)
		code.u2(int(cp.methodref("java/lang/Record", "<init>", "()V")))
		sl := 1
		maxStack := 1
		for _, c := range components {
			code.u1(opAload0)
			loadByDescriptor(code, c.descriptor, sl)
			code.u1(opPutfield)
			code.u2(int(cp.fieldref(name, c.name, c.descriptor)))
			if 1+slotsOf(c.descriptor) > maxStack {
				maxStack = 1 + slotsOf(c.descriptor)
			}
			sl += slotsOf(c.descriptor)
		}
		code.u1(opReturn)
		info := &byteBuffer{}
		if isPublic {
			info.u2(accPublic)
		} else {
			info.u2(0)
		}
		info.u2(int(cp.utf8("<init>")))
		info.u2(int(cp.utf8(string(ctorDescriptor))))
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: maxStack, maxLocals: sl}, "", false, nil, 0)
		return info
	}
	var compact *Node
	var declaredCtors []*Node
	for _, m := range arrayNodes(dr.Members) {
		switch m.Kind {
		case CompactConstructorDeclaration:
			compact = m
		case ConstructorDeclaration:
			declaredCtors = append(declaredCtors, m)
		}
	}
	hasDeclaredCanonical := false
	for _, c := range declaredCtors {
		if joinDescs(ctorParamDescs(c, program)) == joinDescs(componentDescs) {
			hasDeclaredCanonical = true
		}
	}
	if compact != nil {
		f := &NodeFactory{}
		synth := f.NewConstructorDeclaration(nil, nil, nil, &NodeArray{}, nil, compact.AsCompactConstructorDeclaration().Body)
		var compParams []paramSym
		for i, rc := range componentNodes {
			compParams = append(compParams, paramSym{symbol: rc.Symbol, descriptor: components[i].descriptor})
		}
		sl := 1
		var trailing []ctorTrailingStore
		for _, c := range components {
			trailing = append(trailing, ctorTrailingStore{owner: name, name: c.name, descriptor: c.descriptor, slot: sl})
			sl += slotsOf(c.descriptor)
		}
		info := &byteBuffer{}
		if isPublic {
			info.u2(accPublic)
		} else {
			info.u2(0)
		}
		info.u2(int(cp.utf8("<init>")))
		info.u2(int(cp.utf8(string(ctorDescriptor))))
		if body, ok := tryGenerateBody(synth, cp, program, checker, name, bodyGenOptions{ctorSuper: "java/lang/Record", hasCtorSuper: true, lambdaMethods: &lambdaMethods, paramSymbols: compParams, ctorTrailingStores: trailing}); ok {
			writeCodeAttribute(info, cp, body, "", false, nil, 0)
			methods.appendBuf(info)
		} else {
			if degradeListener != nil {
				degradeListener(string(name), "<init>")
			}
			methods.appendBuf(emitImplicitCanonicalCtor())
		}
		methodCount++
	} else if !hasDeclaredCanonical {
		methods.appendBuf(emitImplicitCanonicalCtor())
		methodCount++
	}
	for _, ctor := range declaredCtors {
		methods.appendBuf(emitConstructorMethod(ctor, methodAccessFlags(ctor), cp, program, checker, name, "java/lang/Record", nil, &lambdaMethods, nil))
		methodCount++
	}

	declaredMethodNames := map[string]bool{}
	for _, m := range arrayNodes(dr.Members) {
		if m.Kind == MethodDeclaration {
			declaredMethodNames[m.AsMethodDeclaration().Name.AsIdentifier().Text] = true
		}
	}
	for _, c := range components {
		if declaredMethodNames[c.name] {
			continue
		}
		code := &byteBuffer{}
		code.u1(opAload0)
		code.u1(opGetfield)
		code.u2(int(cp.fieldref(name, c.name, c.descriptor)))
		code.u1(returnOp(c.descriptor))
		info := &byteBuffer{}
		info.u2(accPublic)
		info.u2(int(cp.utf8(c.name)))
		info.u2(int(cp.utf8("()" + string(c.descriptor))))
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: slotsOf(c.descriptor), maxLocals: 1}, "", false, nil, 0)
		methods.appendBuf(info)
		methodCount++
	}

	self := descOf(name)
	var compNames []string
	var getters []recordGetter
	for _, c := range components {
		compNames = append(compNames, c.name)
		getters = append(getters, recordGetter{name: c.name, desc: methodDescriptor("()" + string(c.descriptor))})
	}
	namesJoined := strings.Join(compNames, ";")
	emitObjectMethod := func(mName string, methodDesc, indyDesc methodDescriptor) {
		code := &byteBuffer{}
		code.u1(opAload0)
		if mName == "equals" {
			code.u1(opAload0 + 1)
		}
		code.u1(opInvokeDynamic)
		code.u2(int(cp.invokeDynamicObjectMethod(mName, indyDesc, name, namesJoined, getters)))
		code.u2(0)
		code.u1(returnOp(returnDescriptorOf(methodDesc)))
		info := &byteBuffer{}
		info.u2(accPublic | accFinal)
		info.u2(int(cp.utf8(mName)))
		info.u2(int(cp.utf8(string(methodDesc))))
		ms, ml := 1, 1
		if mName == "equals" {
			ms, ml = 2, 2
		}
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: ms, maxLocals: ml}, "", false, nil, 0)
		methods.appendBuf(info)
		methodCount++
	}
	emitObjectMethod("equals", "(Ljava/lang/Object;)Z", methodDescriptor("("+string(self)+"Ljava/lang/Object;)Z"))
	emitObjectMethod("hashCode", "()I", methodDescriptor("("+string(self)+")I"))
	emitObjectMethod("toString", "()Ljava/lang/String;", methodDescriptor("("+string(self)+")Ljava/lang/String;"))

	for _, member := range arrayNodes(dr.Members) {
		if member.Kind != MethodDeclaration {
			continue
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, 0, nil, ""))
		methodCount++
	}
	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	recordAttr := &byteBuffer{}
	recordAttr.u2(len(components))
	for _, c := range components {
		recordAttr.u2(int(cp.utf8(c.name)))
		recordAttr.u2(int(cp.utf8(string(c.descriptor))))
		recordAttr.u2(0)
	}

	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, "", false, innerClasses, "", nil, &annotationSource{modifiers: declaration.AsRecordDeclaration().Modifiers, from: declaration, program: program})
	attrs.buffer.u2(int(cp.utf8("Record")))
	attrs.buffer.u4(recordAttr.length())
	attrs.buffer.appendBuf(recordAttr)

	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fields, fieldCount, methods, methodCount, attrs.buffer, attrs.count+1),
	}
}

// emitEnum emits an enum declaration (JLS 8.9).
func emitEnum(declaration *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) []EmittedClass {
	program.GetGlobalIndex()
	de := declaration.AsEnumDeclaration()
	var name internalName
	if declaration.Symbol != nil {
		name = binaryName(declaration.Symbol)
	} else {
		name = internalName(de.Name.AsIdentifier().Text)
	}
	selfDesc := descOf(name)
	arrayDesc := descriptor("[" + string(selfDesc))
	const valuesName = "$VALUES"
	superInternalName := internalName("java/lang/Enum")
	var interfaceNames []internalName
	for _, t := range arrayNodes(de.ImplementsTypes) {
		if n := resolveInternalName(t, declaration, program); n != "" {
			interfaceNames = append(interfaceNames, n)
		}
	}
	isPublic := hasModifierKind(de.Modifiers, PublicKeyword)
	// Constants with a body (CONST {...}) each become an E$N subclass; the enum is
	// then not final (it is subclassed) and is implicitly sealed over them. It is
	// abstract iff it declares an abstract method (the two flags are independent).
	var bodied []*Node
	bodyClassNames := map[*Node]internalName{}
	for _, c := range arrayNodes(de.EnumConstants) {
		if c.AsEnumConstantDeclaration().ClassBody != nil {
			bodied = append(bodied, c)
			bodyClassNames[c] = enumBodyClassName(c, program)
		}
	}
	hasAbstractMethod := false
	for _, m := range arrayNodes(de.Members) {
		if m.Kind == MethodDeclaration && hasModifierKind(m.AsMethodDeclaration().Modifiers, AbstractKeyword) {
			hasAbstractMethod = true
			break
		}
	}
	accessFlags := accSuper | accEnum
	if len(bodied) == 0 {
		accessFlags |= accFinal
	}
	if hasAbstractMethod {
		accessFlags |= accAbstract
	}
	if isPublic {
		accessFlags |= accPublic
	}

	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo(string(superInternalName))
	var interfaceIndices []cpIndex
	for _, n := range interfaceNames {
		interfaceIndices = append(interfaceIndices, cp.classInfo(string(n)))
	}

	fieldsBuf, fieldCount := emitFields(declaration, cp, program)
	enumConstants := arrayNodes(de.EnumConstants)
	for _, c := range enumConstants {
		emitFieldInfo(fieldsBuf, cp, accPublic|accStatic|accFinal|accEnum, c.AsEnumConstantDeclaration().Name.AsIdentifier().Text, selfDesc)
		fieldCount++
	}
	emitFieldInfo(fieldsBuf, cp, accPrivate|accStatic|accFinal|accSynthetic, valuesName, arrayDesc)
	fieldCount++

	instanceInits, staticInits := collectFieldInits(arrayNodes(de.Members), name, program)

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer
	var declaredCtors []*Node
	for _, m := range arrayNodes(de.Members) {
		if m.Kind == ConstructorDeclaration {
			declaredCtors = append(declaredCtors, m)
		}
	}
	if len(declaredCtors) == 0 {
		methods.appendBuf(emitEnumConstructor(newDefaultCtorNode(), cp, program, checker, name, instanceInits, &lambdaMethods))
		methodCount++
	} else {
		for _, ctor := range declaredCtors {
			methods.appendBuf(emitEnumConstructor(ctor, cp, program, checker, name, instanceInits, &lambdaMethods))
			methodCount++
		}
	}

	var constants []enumConstantClinit
	for i, c := range enumConstants {
		ec := c.AsEnumConstantDeclaration()
		args := arrayNodes(ec.Arguments)
		var ctor *Node
		for _, k := range declaredCtors {
			if len(arrayNodes(k.AsConstructorDeclaration().Parameters)) == len(args) {
				ctor = k
				break
			}
		}
		var userParamDescs []descriptor
		if ctor != nil {
			userParamDescs = ctorParamDescs(ctor, program)
		}
		constants = append(constants, enumConstantClinit{
			name:           ec.Name.AsIdentifier().Text,
			ordinal:        i,
			ctorDescriptor: methodDescriptor("(Ljava/lang/String;I" + joinDescs(userParamDescs) + ")V"),
			userParamDescs: userParamDescs,
			args:           args,
			ownerInternal:  bodyClassNames[c],
		})
	}

	for _, member := range arrayNodes(de.Members) {
		if member.Kind != MethodDeclaration {
			continue
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, 0, nil, ""))
		methodCount++
	}

	methods.appendBuf(emitValuesMethod(cp, name, valuesName, arrayDesc))
	methodCount++
	methods.appendBuf(emitValueOfMethod(cp, name, selfDesc))
	methodCount++

	clinitInfo := &byteBuffer{}
	clinitInfo.u2(accStatic)
	clinitInfo.u2(int(cp.utf8("<clinit>")))
	clinitInfo.u2(int(cp.utf8("()V")))
	enumClinitData := &enumClinit{enumInternal: name, selfDesc: selfDesc, arrayDesc: arrayDesc, valuesField: valuesName, constants: constants}
	clinitBody, ok := tryGenerateBody(newClinitNode(), cp, program, checker, name, bodyGenOptions{fieldInits: staticInits, lambdaMethods: &lambdaMethods, enumClinit: enumClinitData})
	if !ok {
		if degradeListener != nil {
			degradeListener(string(name), "<clinit>")
		}
		var throwaway []*byteBuffer
		clinitBody, ok = tryGenerateBody(newClinitNode(), cp, program, checker, name, bodyGenOptions{lambdaMethods: &throwaway, enumClinit: enumClinitData})
		if !ok {
			clinitBody = generateBody(newClinitNode(), cp, program, checker, name, bodyGenOptions{lambdaMethods: &throwaway})
		}
	}
	writeCodeAttribute(clinitInfo, cp, clinitBody, "", false, nil, 0)
	methods.appendBuf(clinitInfo)
	methodCount++

	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	sig, hasSig := classSignatureOf(declaration, program)
	// An enum with constant bodies is implicitly sealed over its E$N subclasses,
	// in declaration order (javac's PermittedSubclasses order).
	var permitted []internalName
	for _, c := range bodied {
		permitted = append(permitted, bodyClassNames[c])
	}
	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, sig, hasSig, innerClasses, "", permitted, &annotationSource{modifiers: de.Modifiers, from: declaration, program: program})

	result := []EmittedClass{{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fieldsBuf, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
	}}
	// One E$N subclass per constant body.
	for _, c := range bodied {
		i := indexOfNode(de.EnumConstants, c)
		result = append(result, emitEnumConstantBodyClass(c, bodyClassNames[c], name, constants[i].userParamDescs, program, checker, nestMembers, innerClasses))
	}
	return result
}

// emitEnumConstantBodyClass emits an enum constant body (CONST {...}) as its
// anonymous-style subclass E$N: final, extends the enum, with a private
// constructor that forwards the constant name, ordinal and user arguments to the
// enum's matching constructor. Body methods override the enum's (abstract)
// methods. Mirrors the anonymous-class machinery, but the supertype is the enum
// and the constructor is private.
func emitEnumConstantBodyClass(constant *Node, name, enumInternal internalName, userParamDescs []descriptor, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) EmittedClass {
	body := arrayNodes(constant.AsEnumConstantDeclaration().ClassBody)
	cp := newConstantPool()
	thisClassIndex := cp.classInfo(string(name))
	superClassIndex := cp.classInfo(string(enumInternal))

	// The body's own instance fields (rare). Own-field initializers degrade to
	// defaults (the constructor stays a prologue-only forwarder), as anonymous
	// classes do for unsupported initializers.
	fields, fieldCount := emitFieldsFromMembers(body, cp, program)

	methods := &byteBuffer{}
	methodCount := 0
	var lambdaMethods []*byteBuffer
	// The private constructor forwards (name, ordinal, user args) to the enum's
	// matching <init>, as javac does.
	superParamDescs := append([]descriptor{stringDesc, "I"}, userParamDescs...)
	methods.appendBuf(emitSynthCtor(cp, name, enumInternal, superParamDescs, nil, "", "", accPrivate))
	methodCount++

	// Body methods read the body's own fields through the implicit-`this` getfield
	// path (the body is not a binder container), like anonymous classes.
	captureMap := map[*Symbol]captureField{}
	for _, member := range body {
		if member.Kind != FieldDeclaration {
			continue
		}
		field := member.AsFieldDeclaration()
		if isStaticDeclaration(member) {
			continue
		}
		desc := descriptorOf(field.Type, program, nil)
		for _, d := range arrayNodes(field.Declarators) {
			if d.Symbol != nil {
				captureMap[d.Symbol] = captureField{ownerInternal: name, fieldName: d.AsVariableDeclarator().Name.AsIdentifier().Text, descriptor: desc}
			}
		}
	}
	for _, member := range body {
		if member.Kind != MethodDeclaration {
			continue
		}
		methods.appendBuf(emitMethod(member, cp, program, checker, name, &lambdaMethods, 0, captureMap, ""))
		methodCount++
	}
	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	attrs := buildClassAttributes(cp, sourceNameOf(constant), name, nestMembers, "", false, innerClasses, enumInternal, nil, nil)
	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accSuper|accEnum|accFinal, thisClassIndex, superClassIndex, nil, fields, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
	}
}

// assembleClassFile assembles a class file from its parts (the constant pool must
// be fully populated before this runs).
func assembleClassFile(cp *constantPool, accessFlags int, thisClassIndex, superClassIndex cpIndex, interfaceIndices []cpIndex, fields *byteBuffer, fieldCount int, methods *byteBuffer, methodCount int, attributes *byteBuffer, attributeCount int) []byte {
	out := &byteBuffer{}
	out.u4(classMagic)
	out.u2(minorVersion)
	out.u2(majorVersion)
	cp.writeInto(out)
	out.u2(accessFlags)
	out.u2(int(thisClassIndex))
	out.u2(int(superClassIndex))
	out.u2(len(interfaceIndices))
	for _, index := range interfaceIndices {
		out.u2(int(index))
	}
	out.u2(fieldCount)
	out.appendBuf(fields)
	out.u2(methodCount)
	out.appendBuf(methods)
	out.u2(attributeCount)
	out.appendBuf(attributes)
	return out.toBytes()
}
