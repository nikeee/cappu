package compiler

import (
	"bytes"
	"testing"
)

// Low-level unit checks for the constant pool / byte buffer foundation of the
// bytecode backend. The end-to-end emitter is exercised by the (later) emitter
// tests against javac baselines; these guard the wire encoding directly.

func TestByteBufferModifiedUtf8(t *testing.T) {
	var b byteBuffer
	b.utf8("Aé☃") // ASCII, 2-byte, 3-byte
	want := []byte{0x41, 0xc3, 0xa9, 0xe2, 0x98, 0x83}
	if !bytes.Equal(b.toBytes(), want) {
		t.Errorf("utf8 = % x, want % x", b.toBytes(), want)
	}
	if got := (&byteBuffer{}).utf8Length("Aé☃"); got != 6 {
		t.Errorf("utf8Length = %d, want 6", got)
	}
}

func TestConstantPoolInterningAndWire(t *testing.T) {
	cp := newConstantPool()
	if i := cp.utf8("A"); i != 1 {
		t.Errorf("utf8 index = %d, want 1", i)
	}
	// classInfo reuses the existing "A" utf8 (index 1) and adds the class entry.
	if i := cp.classInfo("A"); i != 2 {
		t.Errorf("classInfo index = %d, want 2", i)
	}
	// dedup: a second classInfo("A") returns the same index, adds nothing.
	if i := cp.classInfo("A"); i != 2 {
		t.Errorf("classInfo dedup index = %d, want 2", i)
	}
	var out byteBuffer
	cp.writeInto(&out)
	// count+1 = 3; then [Utf8 len=1 'A'] [Class nameIndex=1]
	want := []byte{0x00, 0x03, 0x01, 0x00, 0x01, 0x41, 0x07, 0x00, 0x01}
	if !bytes.Equal(out.toBytes(), want) {
		t.Errorf("pool wire = % x, want % x", out.toBytes(), want)
	}
}

func TestConstantPoolLongTakesTwoSlots(t *testing.T) {
	cp := newConstantPool()
	if i := cp.long(1); i != 1 {
		t.Errorf("long index = %d, want 1", i)
	}
	// the next entry lands at index 3 (the long consumed slots 1 and 2).
	if i := cp.utf8("x"); i != 3 {
		t.Errorf("after long, utf8 index = %d, want 3", i)
	}
}

func TestConstantPoolReferencedClassesOrder(t *testing.T) {
	cp := newConstantPool()
	cp.classInfo("java/lang/Object")
	cp.classInfo("java/lang/String")
	cp.classInfo("java/lang/Object") // dedup: not re-recorded
	want := []string{"java/lang/Object", "java/lang/String"}
	if len(cp.referencedClasses) != 2 || cp.referencedClasses[0] != want[0] || cp.referencedClasses[1] != want[1] {
		t.Errorf("referencedClasses = %v, want %v", cp.referencedClasses, want)
	}
}
