package jdwp

import (
	"bytes"
	"math"
	"testing"
)

func TestIDRoundTripEveryWidth(t *testing.T) {
	for _, width := range []int{1, 2, 4, 8} {
		var value uint64 = math.MaxUint64
		if width < 8 {
			value = (uint64(1) << (width * 8)) - 1 // all bits set for that width
		}
		w := &Writer{}
		w.ID(value, width)
		if len(w.Buffer()) != width {
			t.Fatalf("width %d: len %d", width, len(w.Buffer()))
		}
		if got := NewReader(w.Buffer()).ID(width); got != value {
			t.Fatalf("width %d: got %x want %x", width, got, value)
		}
	}
}

func TestI4SignedExtremesAndU4Max(t *testing.T) {
	for _, n := range []int32{math.MinInt32, -1, 0, math.MaxInt32} {
		w := &Writer{}
		w.I4(n)
		if got := NewReader(w.Buffer()).I4(); got != n {
			t.Fatalf("i4 %d -> %d", n, got)
		}
	}
	w := &Writer{}
	w.U4(math.MaxUint32)
	if got := NewReader(w.Buffer()).U4(); got != math.MaxUint32 {
		t.Fatalf("u4 max -> %d", got)
	}
}

func TestU8FullRange(t *testing.T) {
	for _, n := range []uint64{0, 1, 0xdeadbeefcafef00d, math.MaxUint64} {
		w := &Writer{}
		w.U8(n)
		if got := NewReader(w.Buffer()).U8(); got != n {
			t.Fatalf("u8 %x -> %x", n, got)
		}
	}
}

func TestTryReadPacketChunked(t *testing.T) {
	packet := EncodeCommandPacket(7, 1, 1, []byte{0xaa, 0xbb, 0xcc})
	for n := 1; n < len(packet); n++ {
		if _, _, ok := TryReadPacket(packet[:n]); ok {
			t.Fatalf("partial %d bytes should not complete", n)
		}
	}
	p, rest, ok := TryReadPacket(packet)
	if !ok || p.ID != 7 || len(rest) != 0 {
		t.Fatalf("full: ok=%v id=%d rest=%d", ok, p.ID, len(rest))
	}
}

func TestWriterFixedWidthBigEndian(t *testing.T) {
	w := &Writer{}
	w.U1(0x12).U2(0x3456).U4(0x789abcde)
	want := []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde}
	if !bytes.Equal(w.Buffer(), want) {
		t.Fatalf("got %x want %x", w.Buffer(), want)
	}
}

func TestWriterIDWidth(t *testing.T) {
	w := &Writer{}
	w.ID(0x0102, 8)
	if !bytes.Equal(w.Buffer(), []byte{0, 0, 0, 0, 0, 0, 1, 2}) {
		t.Fatalf("8-byte id: got %x", w.Buffer())
	}
	w2 := &Writer{}
	w2.ID(0xab, 4)
	if !bytes.Equal(w2.Buffer(), []byte{0, 0, 0, 0xab}) {
		t.Fatalf("4-byte id: got %x", w2.Buffer())
	}
}

func TestRoundTripEveryField(t *testing.T) {
	w := &Writer{}
	w.U1(200).U2(60000).U4(0xdeadbeef).I4(-5).U8(0x0123456789abcdef).ID(0xcafe, 8).Bool(true).String("héllo")
	r := NewReader(w.Buffer())
	if r.U1() != 200 {
		t.Fatal("u1")
	}
	if r.U2() != 60000 {
		t.Fatal("u2")
	}
	if r.U4() != 0xdeadbeef {
		t.Fatal("u4")
	}
	if r.I4() != -5 {
		t.Fatal("i4")
	}
	if r.U8() != 0x0123456789abcdef {
		t.Fatal("u8")
	}
	if r.ID(8) != 0xcafe {
		t.Fatal("id")
	}
	if !r.Bool() {
		t.Fatal("bool")
	}
	if r.String() != "héllo" {
		t.Fatal("string")
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining %d", r.Remaining())
	}
}

func TestStringLengthCountsBytes(t *testing.T) {
	w := &Writer{}
	w.String("é") // 2 UTF-8 bytes
	if !bytes.Equal(w.Buffer()[:4], []byte{0, 0, 0, 2}) {
		t.Fatalf("len prefix: got %x", w.Buffer()[:4])
	}
}

func TestEncodeDecodeCommandPacket(t *testing.T) {
	data := []byte{1, 2, 3}
	buf := EncodeCommandPacket(0x2a, 1, 7, data)
	if len(buf) != HeaderLen+3 {
		t.Fatalf("len %d", len(buf))
	}
	p := DecodePacket(buf)
	if p.IsReply || p.ID != 0x2a || p.CommandSet != 1 || p.Command != 7 || !bytes.Equal(p.Data, data) {
		t.Fatalf("decoded %+v", p)
	}
}

func TestDecodeReplyPacket(t *testing.T) {
	header := make([]byte, HeaderLen)
	header[0], header[1], header[2], header[3] = 0, 0, 0, HeaderLen
	header[7] = 99 // id
	header[8] = FlagReply
	header[9], header[10] = 0x00, 0x15 // errorCode
	p := DecodePacket(header)
	if !p.IsReply || p.ID != 99 || p.ErrorCode != 0x15 {
		t.Fatalf("decoded %+v", p)
	}
}

func TestTryReadPacketFramesByLength(t *testing.T) {
	a := EncodeCommandPacket(1, 1, 1, []byte{0xaa})
	b := EncodeCommandPacket(2, 1, 1, []byte{0xbb, 0xcc})
	stream := append(append([]byte{}, a...), b...)

	if _, _, ok := TryReadPacket(stream[:HeaderLen]); ok {
		t.Fatal("partial buffer should yield nothing")
	}
	p1, rest, ok := TryReadPacket(stream)
	if !ok || p1.ID != 1 || len(rest) != len(b) {
		t.Fatalf("first: ok=%v id=%d rest=%d", ok, p1.ID, len(rest))
	}
	p2, rest2, ok := TryReadPacket(rest)
	if !ok || p2.ID != 2 || len(rest2) != 0 {
		t.Fatalf("second: ok=%v id=%d rest=%d", ok, p2.ID, len(rest2))
	}
}
