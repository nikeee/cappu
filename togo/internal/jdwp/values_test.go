package jdwp

import (
	"math"
	"testing"
)

var sizes8 = IDSizes{8, 8, 8, 8, 8}
var sizes4 = IDSizes{4, 4, 4, 4, 4}

func decodeValue(build func(*Writer), sizes IDSizes) Value {
	w := &Writer{}
	build(w)
	return ReadValue(NewReader(w.Buffer()), sizes)
}

func TestReadValueBoolean(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagBoolean).U1(1) }, sizes8); !v.Bool || v.Tag != TagBoolean {
		t.Fatalf("true: %+v", v)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagBoolean).U1(0) }, sizes8); v.Bool {
		t.Fatalf("false: %+v", v)
	}
}

func TestReadValueSignExtension(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagByte).U1(0xff) }, sizes8); v.Int != -1 {
		t.Fatalf("byte 0xff -> %d", v.Int)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagByte).U1(0x7f) }, sizes8); v.Int != 127 {
		t.Fatalf("byte 0x7f -> %d", v.Int)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagShort).U2(0xffff) }, sizes8); v.Int != -1 {
		t.Fatalf("short 0xffff -> %d", v.Int)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagShort).U2(0x8000) }, sizes8); v.Int != -32768 {
		t.Fatalf("short 0x8000 -> %d", v.Int)
	}
}

func TestReadValueChar(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagChar).U2(0x0041) }, sizes8); v.Int != 65 {
		t.Fatalf("char -> %d", v.Int)
	}
}

func TestReadValueInt(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagInt).I4(-2147483648) }, sizes8); v.Int != -2147483648 {
		t.Fatalf("int min -> %d", v.Int)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagInt).I4(123456) }, sizes8); v.Int != 123456 {
		t.Fatalf("int -> %d", v.Int)
	}
}

func TestReadValueLong(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagLong).U8(0xffffffffffffffff) }, sizes8); v.Int != -1 {
		t.Fatalf("long -1 -> %d", v.Int)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagLong).U8(0x7fffffffffffffff) }, sizes8); v.Int != 9223372036854775807 {
		t.Fatalf("long max -> %d", v.Int)
	}
}

func TestReadValueFloatDouble(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagFloat).U4(math.Float32bits(1.5)) }, sizes8); v.Float != 1.5 {
		t.Fatalf("float -> %v", v.Float)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagDouble).U8(math.Float64bits(-2.25)) }, sizes8); v.Float != -2.25 {
		t.Fatalf("double -> %v", v.Float)
	}
}

func TestReadValueObjectTags(t *testing.T) {
	for _, tag := range []byte{TagObject, TagString, TagArray, TagThread} {
		v := decodeValue(func(w *Writer) { w.U1(tag).ID(0xabc, 8) }, sizes8)
		if !v.Object || v.ObjectID != 0xabc || v.Tag != tag {
			t.Fatalf("tag %d -> %+v", tag, v)
		}
	}
}

func TestReadValueNullAndNonDefaultWidth(t *testing.T) {
	if v := decodeValue(func(w *Writer) { w.U1(TagObject).ID(0, 8) }, sizes8); !v.Object || v.ObjectID != 0 {
		t.Fatalf("null -> %+v", v)
	}
	if v := decodeValue(func(w *Writer) { w.U1(TagObject).ID(0xdead, 4) }, sizes4); v.ObjectID != 0xdead {
		t.Fatalf("4-byte id -> %+v", v)
	}
}

func TestLocationRoundTrip(t *testing.T) {
	loc := Location{TypeTag: TypeTagClass, ClassID: 0xc1, MethodID: 0xa1, Index: 42}
	for _, sizes := range []IDSizes{sizes8, sizes4} {
		w := &Writer{}
		writeLocation(w, sizes, loc)
		if got := readLocation(NewReader(w.Buffer()), sizes); got != loc {
			t.Fatalf("sizes %+v: got %+v want %+v", sizes, got, loc)
		}
	}
}
