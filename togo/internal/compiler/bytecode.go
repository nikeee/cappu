package compiler

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
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

// byteBuffer is a growable big-endian byte buffer.
type byteBuffer struct {
	bytes []byte
}

func (b *byteBuffer) u1(v int) {
	b.bytes = append(b.bytes, byte(v&0xff))
}

func (b *byteBuffer) u2(v int) {
	b.bytes = append(b.bytes, byte((v>>8)&0xff), byte(v&0xff))
}

func (b *byteBuffer) u4(v int) {
	b.bytes = append(b.bytes, byte((v>>24)&0xff), byte((v>>16)&0xff), byte((v>>8)&0xff), byte(v&0xff))
}

// utf8 writes modified UTF-8 (JVMS 4.4.7). ASCII and the BMP are handled by
// iterating UTF-16 code units (matching the TS charCodeAt loop byte-for-byte).
func (b *byteBuffer) utf8(s string) {
	for _, c := range utf16.Encode([]rune(s)) {
		switch {
		case c >= 0x01 && c <= 0x7f:
			b.bytes = append(b.bytes, byte(c))
		case c <= 0x7ff:
			b.bytes = append(b.bytes, byte(0xc0|(c>>6)), byte(0x80|(c&0x3f)))
		default:
			b.bytes = append(b.bytes, byte(0xe0|(c>>12)), byte(0x80|((c>>6)&0x3f)), byte(0x80|(c&0x3f)))
		}
	}
}

func (b *byteBuffer) utf8Length(s string) int {
	n := 0
	for _, c := range utf16.Encode([]rune(s)) {
		switch {
		case c >= 0x01 && c <= 0x7f:
			n++
		case c <= 0x7ff:
			n += 2
		default:
			n += 3
		}
	}
	return n
}

func (b *byteBuffer) appendBuf(other *byteBuffer) {
	b.bytes = append(b.bytes, other.bytes...)
}

func (b *byteBuffer) toBytes() []byte {
	return b.bytes
}

func (b *byteBuffer) length() int {
	return len(b.bytes)
}

// patchU2 overwrites a previously-reserved u2 (for branch-offset backpatching).
func (b *byteBuffer) patchU2(pos, value int) {
	b.bytes[pos] = byte((value >> 8) & 0xff)
	b.bytes[pos+1] = byte(value & 0xff)
}

// patchU4 overwrites a previously-reserved u4 (for tableswitch/lookupswitch offsets).
func (b *byteBuffer) patchU4(pos, value int) {
	b.bytes[pos] = byte((value >> 24) & 0xff)
	b.bytes[pos+1] = byte((value >> 16) & 0xff)
	b.bytes[pos+2] = byte((value >> 8) & 0xff)
	b.bytes[pos+3] = byte(value & 0xff)
}

// cpIndex is an index into a class's constant pool (1-based; JVMS 4.1).
type cpIndex int

// bootstrapEntry is one BootstrapMethods entry (JVMS 4.7.23): a bootstrap
// MethodHandle plus its static arguments.
type bootstrapEntry struct {
	handle cpIndex
	args   []cpIndex
}

// constantPool builds the constant pool, interning entries so each appears once.
// Indices are 1-based (JVMS 4.1: the pool is indexed 1..count-1).
type constantPool struct {
	entries byteBuffer
	count   int // number of entries (next index is count + 1)
	cache   map[string]cpIndex
	// bootstraps backs the BootstrapMethods attribute.
	bootstraps []bootstrapEntry
	// referencedClasses is every class named by a CONSTANT_Class entry so far,
	// in interning order (InternalName or Descriptor strings).
	referencedClasses []string
}

func newConstantPool() *constantPool {
	return &constantPool{cache: map[string]cpIndex{}}
}

func (cp *constantPool) intern(key string, write func(*byteBuffer), wide bool) cpIndex {
	if existing, ok := cp.cache[key]; ok {
		return existing
	}
	write(&cp.entries)
	cp.count++ // the single producing increment
	index := cpIndex(cp.count)
	if wide {
		cp.count++ // long/double occupy a second, unusable slot (JVMS 4.4.5)
	}
	cp.cache[key] = index
	return index
}

func (cp *constantPool) utf8(value string) cpIndex {
	return cp.intern("u:"+value, func(b *byteBuffer) {
		b.u1(constantUtf8)
		b.u2(b.utf8Length(value))
		b.utf8(value)
	}, false)
}

// classInfo accepts an InternalName or a Descriptor: checkcast/anewarray name
// array CLASSES by their descriptor (JVMS 4.4.1).
func (cp *constantPool) classInfo(name string) cpIndex {
	if _, ok := cp.cache["c:"+name]; !ok {
		cp.referencedClasses = append(cp.referencedClasses, name)
	}
	nameIndex := cp.utf8(name)
	return cp.intern("c:"+name, func(b *byteBuffer) {
		b.u1(constantClass)
		b.u2(int(nameIndex))
	}, false)
}

func (cp *constantPool) nameAndType(name, desc string) cpIndex {
	nameIndex := cp.utf8(name)
	descIndex := cp.utf8(desc)
	return cp.intern("nt:"+name+":"+desc, func(b *byteBuffer) {
		b.u1(constantNameAndType)
		b.u2(int(nameIndex))
		b.u2(int(descIndex))
	}, false)
}

// methodref's class may be an array class, named by its descriptor (JVMS 4.4.2),
// e.g. int[].clone() - so it accepts an InternalName or Descriptor string.
func (cp *constantPool) methodref(internalClass, name string, desc methodDescriptor) cpIndex {
	classIndex := cp.classInfo(internalClass)
	ntIndex := cp.nameAndType(name, string(desc))
	return cp.intern("m:"+internalClass+":"+name+":"+string(desc), func(b *byteBuffer) {
		b.u1(constantMethodref)
		b.u2(int(classIndex))
		b.u2(int(ntIndex))
	}, false)
}

func (cp *constantPool) interfaceMethodref(internalClass internalName, name string, desc methodDescriptor) cpIndex {
	classIndex := cp.classInfo(string(internalClass))
	ntIndex := cp.nameAndType(name, string(desc))
	return cp.intern("im:"+string(internalClass)+":"+name+":"+string(desc), func(b *byteBuffer) {
		b.u1(constantInterfaceMethodref)
		b.u2(int(classIndex))
		b.u2(int(ntIndex))
	}, false)
}

func (cp *constantPool) fieldref(internalClass internalName, name string, desc descriptor) cpIndex {
	classIndex := cp.classInfo(string(internalClass))
	ntIndex := cp.nameAndType(name, string(desc))
	return cp.intern("f:"+string(internalClass)+":"+name+":"+string(desc), func(b *byteBuffer) {
		b.u1(constantFieldref)
		b.u2(int(classIndex))
		b.u2(int(ntIndex))
	}, false)
}

func (cp *constantPool) stringConst(value string) cpIndex {
	utf8Index := cp.utf8(value)
	return cp.intern("s:"+value, func(b *byteBuffer) {
		b.u1(constantString)
		b.u2(int(utf8Index))
	}, false)
}

func (cp *constantPool) integer(value int) cpIndex {
	return cp.intern(fmt.Sprintf("i:%d", value), func(b *byteBuffer) {
		b.u1(constantInteger)
		b.u4(value)
	}, false)
}

func (cp *constantPool) long(value int64) cpIndex {
	// Long occupies two pool entries (JVMS 4.4.5).
	return cp.intern(fmt.Sprintf("l:%d", value), func(b *byteBuffer) {
		b.u1(constantLong)
		b.u4(int(uint32(uint64(value) >> 32)))
		b.u4(int(uint32(uint64(value))))
	}, true)
}

func (cp *constantPool) float(value float64) cpIndex {
	bits := math.Float32bits(float32(value))
	return cp.intern(fmt.Sprintf("f:%d", bits), func(b *byteBuffer) {
		b.u1(constantFloat)
		b.u4(int(bits))
	}, false)
}

func (cp *constantPool) double(value float64) cpIndex {
	bits := math.Float64bits(value)
	hi := uint32(bits >> 32)
	lo := uint32(bits)
	return cp.intern(fmt.Sprintf("d:%d:%d", hi, lo), func(b *byteBuffer) {
		b.u1(constantDouble)
		b.u4(int(hi))
		b.u4(int(lo))
	}, true)
}

func (cp *constantPool) methodHandle(referenceKind int, referenceIndex cpIndex) cpIndex {
	return cp.intern(fmt.Sprintf("mh:%d:%d", referenceKind, referenceIndex), func(b *byteBuffer) {
		b.u1(constantMethodHandle)
		b.u1(referenceKind)
		b.u2(int(referenceIndex))
	}, false)
}

// invokeDynamicConcat is an invokedynamic to StringConcatFactory.makeConcatWithConstants
// with the given recipe and dynamic-argument descriptor. Registers the bootstrap
// method and returns the CONSTANT_InvokeDynamic index.
func (cp *constantPool) invokeDynamicConcat(recipe, dynamicArgsDescriptor string) cpIndex {
	handle := cp.methodHandle(refInvokeStatic,
		cp.methodref(string(stringConcatFactory), makeConcat, makeConcatBsmDescriptor))
	recipeIndex := cp.stringConst(recipe)
	bootstrapIndex := len(cp.bootstraps)
	cp.bootstraps = append(cp.bootstraps, bootstrapEntry{handle: handle, args: []cpIndex{recipeIndex}})
	nt := cp.nameAndType(makeConcat, "("+dynamicArgsDescriptor+")Ljava/lang/String;")
	return cp.intern(fmt.Sprintf("indy:%d:%s", bootstrapIndex, dynamicArgsDescriptor), func(b *byteBuffer) {
		b.u1(constantInvokeDynamic)
		b.u2(bootstrapIndex)
		b.u2(int(nt))
	}, false)
}

type recordGetter struct {
	name string
	desc methodDescriptor
}

// invokeDynamicObjectMethod is an invokedynamic to ObjectMethods.bootstrap for a
// record's equals/hashCode/toString.
func (cp *constantPool) invokeDynamicObjectMethod(methodName string, desc methodDescriptor, recordInternal internalName, names string, getters []recordGetter) cpIndex {
	bsmHandle := cp.methodHandle(refInvokeStatic,
		cp.methodref("java/lang/runtime/ObjectMethods", "bootstrap", objectMethodsBsmDescriptor))
	args := []cpIndex{cp.classInfo(string(recordInternal)), cp.stringConst(names)}
	for _, g := range getters {
		args = append(args, cp.methodHandle(refInvokeVirtual, cp.methodref(string(recordInternal), g.name, g.desc)))
	}
	bootstrapIndex := len(cp.bootstraps)
	cp.bootstraps = append(cp.bootstraps, bootstrapEntry{handle: bsmHandle, args: args})
	nt := cp.nameAndType(methodName, string(desc))
	return cp.intern(fmt.Sprintf("indy:%d:%s:%s", bootstrapIndex, methodName, desc), func(b *byteBuffer) {
		b.u1(constantInvokeDynamic)
		b.u2(bootstrapIndex)
		b.u2(int(nt))
	}, false)
}

func (cp *constantPool) methodType(desc methodDescriptor) cpIndex {
	descIndex := cp.utf8(string(desc))
	return cp.intern("mt:"+string(desc), func(b *byteBuffer) {
		b.u1(constantMethodType)
		b.u2(int(descIndex))
	}, false)
}

// invokeDynamicLambda builds a lambda via LambdaMetafactory.metafactory.
func (cp *constantPool) invokeDynamicLambda(samName string, indyDescriptor, samErased methodDescriptor, implRefKind int, implOwner internalName, implName string, implDescriptor, instantiated methodDescriptor, implIsInterface bool) cpIndex {
	bsmHandle := cp.methodHandle(refInvokeStatic,
		cp.methodref(string(lambdaMetafactory), "metafactory", lambdaMetafactoryBsmDescriptor))
	// The impl reference: a constructor (<init>) or a normal method, on a class or interface.
	var implRef cpIndex
	switch {
	case implName == "<init>":
		implRef = cp.methodref(string(implOwner), implName, implDescriptor)
	case implIsInterface:
		implRef = cp.interfaceMethodref(implOwner, implName, implDescriptor)
	default:
		implRef = cp.methodref(string(implOwner), implName, implDescriptor)
	}
	implHandle := cp.methodHandle(implRefKind, implRef)
	args := []cpIndex{cp.methodType(samErased), implHandle, cp.methodType(instantiated)}
	bootstrapIndex := len(cp.bootstraps)
	cp.bootstraps = append(cp.bootstraps, bootstrapEntry{handle: bsmHandle, args: args})
	nt := cp.nameAndType(samName, string(indyDescriptor))
	return cp.intern(fmt.Sprintf("indy:%d:%s:%s", bootstrapIndex, samName, indyDescriptor), func(b *byteBuffer) {
		b.u1(constantInvokeDynamic)
		b.u2(bootstrapIndex)
		b.u2(int(nt))
	}, false)
}

func (cp *constantPool) bootstrapMethodCount() int {
	return len(cp.bootstraps) // a count, not a pool index
}

// bootstrapMethodsBody is the BootstrapMethods attribute body.
func (cp *constantPool) bootstrapMethodsBody() *byteBuffer {
	body := &byteBuffer{}
	body.u2(len(cp.bootstraps))
	for _, bm := range cp.bootstraps {
		body.u2(int(bm.handle))
		body.u2(len(bm.args))
		for _, a := range bm.args {
			body.u2(int(a))
		}
	}
	return body
}

// writeInto writes constant_pool_count followed by the pool entries.
func (cp *constantPool) writeInto(out *byteBuffer) {
	out.u2(cp.count + 1)
	out.appendBuf(&cp.entries)
}

// --- small shared helpers ---------------------------------------------------

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

// fieldInfo is a resolved field/enum-constant reference: where it lives and its descriptor.
type fieldInfo struct {
	owner      internalName
	name       string
	descriptor descriptor
	isStatic   bool
}

// localSlotInfo is a local variable / parameter slot and its descriptor
// (long/double take two slots but one entry).
type localSlotInfo struct {
	slot       slot
	descriptor descriptor
	// Debug info (LocalVariableTable), only under emitDebugInfo.
	name        string
	lvtStart    pc
	hasLvtStart bool
}

// exceptionTableEntry is one Code-attribute exception_table entry (JVMS 4.7.3).
// catchType 0 is a catch-all (used for finally).
type exceptionTableEntry struct {
	start, end, handler pc
	catchType           cpIndex // class cp entry of the caught type, or 0 for catch-all
}

// finallyAction is cleanup an abrupt exit must run on the way out. kind is
// "block" (a user finally), "resource" (try-with-resources close()), or "monitor".
type finallyAction struct {
	kind          string
	block         *Node
	slot          int
	ownerInternal internalName
	isInterface   bool
	guarded       bool
}

// lambdaImpl is a synthetic method holding a lambda body.
type lambdaImpl struct {
	name             string
	params           []paramSym
	returnDescriptor descriptor
	body             *Node
	isInstance       bool
}

// enumConstantClinit describes how to construct one enum constant.
type enumConstantClinit struct {
	name           string
	ordinal        int
	ctorDescriptor methodDescriptor // (Ljava/lang/String;I<userparams>)V
	userParamDescs []descriptor
	args           []*Node
}

// enumClinit is the data the enum <clinit> needs.
type enumClinit struct {
	enumInternal internalName
	selfDesc     descriptor // L<enum>;
	arrayDesc    descriptor // [L<enum>;
	valuesField  string     // synthetic $VALUES field name
	constants    []enumConstantClinit
}

// --- class attributes + nest/inner-class computation ------------------------

func writeSignatureAttribute(info *byteBuffer, cp *constantPool, signature jvmSignature) {
	index := cp.utf8(string(signature))
	info.u2(int(cp.utf8("Signature")))
	info.u4(2)
	info.u2(int(index))
}

// innerClassMap is an insertion-ordered map of inner-class records; the order is
// significant (innerClassOrder iterates members in declaration order).
type innerClassMap struct {
	keys   []internalName
	values map[internalName]innerClassRecord
}

func newInnerClassMap() *innerClassMap {
	return &innerClassMap{values: map[internalName]innerClassRecord{}}
}

func (m *innerClassMap) set(k internalName, v innerClassRecord) {
	if _, ok := m.values[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.values[k] = v
}

func (m *innerClassMap) get(k internalName) (innerClassRecord, bool) {
	v, ok := m.values[k]
	return v, ok
}

func (m *innerClassMap) has(k internalName) bool {
	_, ok := m.values[k]
	return ok
}

func (m *innerClassMap) len() int { return len(m.keys) }

func cutAtDollar(name internalName) internalName {
	if i := strings.IndexByte(string(name), '$'); i >= 0 {
		return name[:i]
	}
	return name
}

// classAttributes is the result of buildClassAttributes.
type classAttributes struct {
	buffer *byteBuffer
	count  int
}

// buildClassAttributes builds the class-level attributes shared by classes and
// enums: Signature, SourceFile, BootstrapMethods, NestHost/NestMembers and
// InnerClasses. Must run before the constant pool is written so the attribute
// name Utf8s are interned.
func buildClassAttributes(cp *constantPool, sourceName string, name internalName, nestMembers map[string][]internalName, signature jvmSignature, hasSignature bool, innerClasses *innerClassMap) classAttributes {
	buffer := &byteBuffer{}
	count := 0
	refCountBeforeAttrs := len(cp.referencedClasses)
	if hasSignature {
		writeSignatureAttribute(buffer, cp, signature)
		count++
	}
	if sourceName != "" {
		buffer.u2(int(cp.utf8("SourceFile")))
		buffer.u4(2)
		buffer.u2(int(cp.utf8(sourceName)))
		count++
	}
	if cp.bootstrapMethodCount() > 0 {
		buffer.u2(int(cp.utf8("BootstrapMethods")))
		body := cp.bootstrapMethodsBody()
		buffer.u4(body.length())
		buffer.appendBuf(body)
		count++
	}
	if name != "" && nestMembers != nil {
		host := cutAtDollar(name)
		if name == host {
			var members []internalName
			for _, n := range nestMembers[string(host)] {
				if n != host {
					members = append(members, n)
				}
			}
			if len(members) > 0 {
				buffer.u2(int(cp.utf8("NestMembers")))
				buffer.u4(2 + 2*len(members))
				buffer.u2(len(members))
				for _, mm := range members {
					buffer.u2(int(cp.classInfo(string(mm))))
				}
				count++
			}
		} else {
			buffer.u2(int(cp.utf8("NestHost")))
			buffer.u4(2)
			buffer.u2(int(cp.classInfo(string(host))))
			count++
		}
	}
	if innerClasses != nil && innerClasses.len() > 0 {
		order := innerClassOrder(name, cp.referencedClasses, refCountBeforeAttrs, innerClasses, cp.bootstrapMethodCount() > 0)
		if len(order) > 0 {
			buffer.u2(int(cp.utf8("InnerClasses")))
			buffer.u4(2 + 8*len(order))
			buffer.u2(len(order))
			for _, n := range order {
				record, _ := innerClasses.get(n)
				buffer.u2(int(cp.classInfo(string(n))))
				if record.outer != "" {
					buffer.u2(int(cp.classInfo(string(record.outer))))
				} else {
					buffer.u2(0)
				}
				if record.simpleName != "" {
					buffer.u2(int(cp.utf8(record.simpleName)))
				} else {
					buffer.u2(0)
				}
				buffer.u2(record.flags)
			}
			count++
		}
	}
	return classAttributes{buffer: buffer, count: count}
}

// computeNestMembers is the nest grouping of a source file: host binary name ->
// every member of that nest (including the host).
func computeNestMembers(sourceFile *Node, program *Program) map[string][]internalName {
	program.GetGlobalIndex()
	byHost := map[string][]internalName{}
	add := func(n internalName) {
		host := string(cutAtDollar(n))
		byHost[host] = append(byHost[host], n)
	}
	var visit func(node *Node)
	visit = func(node *Node) {
		switch {
		case node.Kind == ClassDeclaration:
			d := node.AsClassDeclaration()
			if node.Symbol != nil {
				add(binaryName(node.Symbol))
			} else if d.Name != nil {
				add(internalName(d.Name.AsIdentifier().Text))
			}
		case node.Kind == InterfaceDeclaration || node.Kind == EnumDeclaration || node.Kind == RecordDeclaration:
			if node.Symbol != nil {
				add(binaryName(node.Symbol))
			}
		case node.Kind == ObjectCreationExpression && node.AsObjectCreationExpression().ClassBody != nil && anonymousTarget(node, program) != nil:
			add(anonymousClassName(node, program))
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	visit(sourceFile)
	return byHost
}

// computeInnerClassInfo returns every nested class declared in the file, keyed by
// binary name - the data an InnerClasses entry needs (JVMS 4.7.6).
func computeInnerClassInfo(sourceFile *Node, program *Program) *innerClassMap {
	program.GetGlobalIndex()
	info := newInnerClassMap()
	var visit func(node *Node)
	visit = func(node *Node) {
		switch {
		case isTypeDeclarationKind(node.Kind) && node.Symbol != nil:
			name := binaryName(node.Symbol)
			if strings.Contains(string(name), "$") {
				memberOfType := node.Parent != nil && isTypeDeclarationKind(node.Parent.Kind)
				outer := internalName("")
				if memberOfType {
					outer = binaryName(node.Parent.Symbol)
				}
				simpleName := ""
				if nm := nodeName(node); nm != nil {
					simpleName = nm.AsIdentifier().Text
				}
				info.set(name, innerClassRecord{outer: outer, simpleName: simpleName, flags: innerClassFlags(node)})
			}
		case node.Kind == ObjectCreationExpression && node.AsObjectCreationExpression().ClassBody != nil && anonymousTarget(node, program) != nil:
			info.set(anonymousClassName(node, program), innerClassRecord{flags: 0})
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	visit(sourceFile)
	return info
}

// innerClassOrder returns the order javac writes InnerClasses entries (JVMS 4.7.6).
func innerClassOrder(thisName internalName, referencedClasses []string, refCountBeforeAttrs int, innerClasses *innerClassMap, hasBootstrap bool) []internalName {
	if hasBootstrap && !innerClasses.has(lookupName) {
		innerClasses.set(lookupName, innerClassRecord{
			outer:      "java/lang/invoke/MethodHandles",
			simpleName: "Lookup",
			flags:      accPublic | accStatic | accFinal,
		})
	}
	var result []internalName
	seen := map[internalName]bool{}
	var enter func(n internalName)
	enter = func(n internalName) {
		if seen[n] {
			return
		}
		record, ok := innerClasses.get(n)
		if !ok {
			return // not a known nested class: no InnerClasses entry
		}
		if record.outer != "" && innerClasses.has(record.outer) {
			enter(record.outer)
		}
		if seen[n] {
			return
		}
		seen[n] = true
		result = append(result, n)
	}
	// Pass 1: classes referenced while writing the class body, in intern order.
	limit := refCountBeforeAttrs
	if len(referencedClasses) < limit {
		limit = len(referencedClasses)
	}
	for i := 0; i < limit; i++ {
		n := internalName(referencedClasses[i])
		if n != thisName {
			enter(n)
		}
	}
	if thisName != "" {
		enter(thisName)
	}
	// Pass 2: the declared-member tree rooted at this class, breadth-first, each
	// level in reverse-declaration order.
	referenced := map[internalName]bool{}
	for _, c := range referencedClasses {
		referenced[internalName(c)] = true
	}
	childrenReverse := func(owner internalName) []internalName {
		var kids []internalName
		for _, name := range innerClasses.keys {
			if record := innerClasses.values[name]; record.outer == owner {
				kids = append(kids, name)
			}
		}
		// reverse
		for i, j := 0, len(kids)-1; i < j; i, j = i+1, j-1 {
			kids[i], kids[j] = kids[j], kids[i]
		}
		return kids
	}
	type queueItem struct {
		n     internalName
		depth int
	}
	var queue []queueItem
	for _, n := range childrenReverse(thisName) {
		queue = append(queue, queueItem{n: n, depth: 1})
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.depth == 1 || referenced[item.n] {
			enter(item.n)
		}
		for _, c := range childrenReverse(item.n) {
			queue = append(queue, queueItem{n: c, depth: item.depth + 1})
		}
	}
	if hasBootstrap {
		enter(lookupName)
	}
	return result
}

// --- method body code generation (generateBody) -----------------------------

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
	offset              pc // -1 until placed
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
			g.lineSource = &lineSourceInfo{text: sf.Text, starts: ComputeLineStarts(sf.Text)}
		}
	}
	if g.lineSource == nil {
		return
	}
	start := SkipTrivia(g.lineSource.text, node.Pos)
	if start >= len(g.lineSource.text) {
		return
	}
	line := GetLineAndCharacterOfPosition(g.lineSource.starts, start).Line + 1 // 1-based
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
	if callee.Kind != PropertyAccessExpression {
		return "", false
	}
	access := callee.AsPropertyAccessExpression()
	if access.Expression.Kind != Identifier {
		return "", false
	}
	recv := ResolveTypeEntityName(access.Expression, access.Expression, g.program)
	if recv == nil || recv.Flags&SymbolFlagsEnum == 0 {
		return "", false
	}
	enumInternal := binaryName(recv)
	mname := access.Name.AsIdentifier().Text
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

// --- anonymous class targeting -----------------------------------------------

func anonymousClassName(node *Node, program *Program) internalName {
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
		if n.Kind == ObjectCreationExpression && n.AsObjectCreationExpression().ClassBody != nil && n.Pos <= node.Pos {
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
	idx := g.cp.invokeDynamicLambda(info.SamName, indyDescriptor, samErased, refKind, g.thisInternalName, implName, implDescriptor, instantiated, false)
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
		idx := g.cp.invokeDynamicLambda(info.SamName, methodDescriptor("()"+string(interfaceDesc)), samErased,
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
	idx := g.cp.invokeDynamicLambda(info.SamName, methodDescriptor("("+string(dynamicArgs)+")"+string(interfaceDesc)), samErased,
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
		g.code.u1(opNew)
		g.code.u2(int(g.cp.classInfo(string(ec.enumInternal))))
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
		g.code.u2(int(g.cp.methodref(string(ec.enumInternal), "<init>", c.ctorDescriptor)))
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

	if flags&(accAbstract|accNative) != 0 || method.AsMethodDeclaration().Body == nil {
		if hasSignature {
			info.u2(1)
			writeSignatureAttribute(info, cp, signature)
		} else {
			info.u2(0)
		}
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

	writeCodeAttribute(info, cp, body, signature, hasSignature)
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
	writeCodeAttribute(info, cp, body, "", false)
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
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 1, maxLocals: 1}, "", false)
	return info
}

// writeCodeAttribute appends the Code attribute (with optional StackMapTable) and,
// when generic, a Signature attribute (JVMS 4.7.9).
func writeCodeAttribute(info *byteBuffer, cp *constantPool, body compiledMethod, signature jvmSignature, hasSignature bool) {
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

	if hasSignature {
		info.u2(2)
	} else {
		info.u2(1)
	}
	info.appendBuf(codeAttr)
	if hasSignature {
		writeSignatureAttribute(info, cp, signature)
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
	writeCodeAttribute(info, cp, body, signature, hasSignature)
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
func emitSynthCtor(cp *constantPool, name, superInternal internalName, superParamDescs []descriptor, captures []localCapture, this0Descriptor, superThis0Descriptor descriptor) *byteBuffer {
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
	info.u2(0)
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
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: maxStack, maxLocals: sl}, "", false)
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
		return emitSynthCtor(cp, name, prologue.superInternal, prologue.superParamDescs, prologue.captures, prologue.this0Descriptor, "")
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
	writeCodeAttribute(info, cp, body, "", false)
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
			methods.appendBuf(emitSynthCtor(cp, name, superInternalName, nil, localCaptures, this0Descriptor, ""))
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
		writeCodeAttribute(info, cp, clinitBody, "", false)
		methods.appendBuf(info)
		methodCount++
	}

	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	sig, hasSig := classSignatureOf(declaration, program)
	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, sig, hasSig, innerClasses)

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

	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, "", false, innerClasses)
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
		methods.appendBuf(emitSynthCtor(cp, name, target.superInternal, target.superParamDescs, captures, this0Descriptor, target.superThis0Desc))
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

	attrs := buildClassAttributes(cp, sourceNameOf(node), name, nestMembers, "", false, innerClasses)
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
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 1, maxLocals: 0}, "", false)
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
	writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: 2, maxLocals: 1}, "", false)
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
	writeCodeAttribute(info, cp, body, "", false)
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
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: maxStack, maxLocals: sl}, "", false)
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
			writeCodeAttribute(info, cp, body, "", false)
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
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: slotsOf(c.descriptor), maxLocals: 1}, "", false)
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
		writeCodeAttribute(info, cp, compiledMethod{code: code, maxStack: ms, maxLocals: ml}, "", false)
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

	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, "", false, innerClasses)
	attrs.buffer.u2(int(cp.utf8("Record")))
	attrs.buffer.u4(recordAttr.length())
	attrs.buffer.appendBuf(recordAttr)

	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fields, fieldCount, methods, methodCount, attrs.buffer, attrs.count+1),
	}
}

// emitEnum emits an enum declaration (JLS 8.9).
func emitEnum(declaration *Node, program *Program, checker *Checker, nestMembers map[string][]internalName, innerClasses *innerClassMap) EmittedClass {
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
	accessFlags := accSuper | accEnum | accFinal
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
	writeCodeAttribute(clinitInfo, cp, clinitBody, "", false)
	methods.appendBuf(clinitInfo)
	methodCount++

	for _, impl := range lambdaMethods {
		methods.appendBuf(impl)
		methodCount++
	}

	sig, hasSig := classSignatureOf(declaration, program)
	attrs := buildClassAttributes(cp, sourceNameOf(declaration), name, nestMembers, sig, hasSig, innerClasses)

	return EmittedClass{
		Name:  string(name),
		Bytes: assembleClassFile(cp, accessFlags, thisClassIndex, superClassIndex, interfaceIndices, fieldsBuf, fieldCount, methods, methodCount, attrs.buffer, attrs.count),
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
