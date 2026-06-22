package compiler

import (
	"fmt"
	"math"
)

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
