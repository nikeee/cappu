// Package jdwp implements a JDWP (Java Debug Wire Protocol) client for cappu's
// debug adapter: byte/packet codecs, the connection, typed command wrappers and
// event decoding.
package jdwp

// Big-endian byte reader/writer for the JDWP wire protocol. JDWP sizes its
// reference IDs (objectID, methodID, ...) per the VM's VirtualMachine.IDSizes
// reply, so reads/writes of those take the negotiated width; every other field
// is fixed-width big-endian. IDs are kept as uint64 to hold the full 8-byte
// range. Port of src/jdwp/idCodec.ts.

import "encoding/binary"

// IDSizes is the per-VM byte width of each JDWP reference id kind.
type IDSizes struct {
	FieldID         int
	MethodID        int
	ObjectID        int
	ReferenceTypeID int
	FrameID         int
}

// DefaultIDSizes is the placeholder used before VirtualMachine.IDSizes is read
// (8 bytes is the modern HotSpot default).
var DefaultIDSizes = IDSizes{FieldID: 8, MethodID: 8, ObjectID: 8, ReferenceTypeID: 8, FrameID: 8}

// Writer accumulates big-endian JDWP fields.
type Writer struct{ buf []byte }

func (w *Writer) U1(n byte) *Writer { w.buf = append(w.buf, n); return w }

func (w *Writer) U2(n uint16) *Writer {
	w.buf = binary.BigEndian.AppendUint16(w.buf, n)
	return w
}

func (w *Writer) U4(n uint32) *Writer {
	w.buf = binary.BigEndian.AppendUint32(w.buf, n)
	return w
}

func (w *Writer) I4(n int32) *Writer { return w.U4(uint32(n)) }

func (w *Writer) U8(n uint64) *Writer { return w.ID(n, 8) }

func (w *Writer) Bool(b bool) *Writer {
	if b {
		return w.U1(1)
	}
	return w.U1(0)
}

// ID writes a size-byte big-endian id (size from the negotiated IDSizes).
func (w *Writer) ID(v uint64, size int) *Writer {
	tmp := make([]byte, size)
	for i := size - 1; i >= 0; i-- {
		tmp[i] = byte(v & 0xff)
		v >>= 8
	}
	w.buf = append(w.buf, tmp...)
	return w
}

// String writes a JDWP string: u4 byte length of the UTF-8 bytes, then the bytes.
func (w *Writer) String(s string) *Writer {
	w.U4(uint32(len(s)))
	w.buf = append(w.buf, s...)
	return w
}

func (w *Writer) Bytes(b []byte) *Writer { w.buf = append(w.buf, b...); return w }

func (w *Writer) Len() int       { return len(w.buf) }
func (w *Writer) Buffer() []byte { return w.buf }

// Reader consumes big-endian JDWP fields from a packet body.
type Reader struct {
	buf []byte
	off int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

func (r *Reader) U1() byte {
	v := r.buf[r.off]
	r.off++
	return v
}

func (r *Reader) U2() uint16 {
	v := binary.BigEndian.Uint16(r.buf[r.off:])
	r.off += 2
	return v
}

func (r *Reader) U4() uint32 {
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v
}

func (r *Reader) I4() int32 { return int32(r.U4()) }

func (r *Reader) U8() uint64 { return r.ID(8) }

func (r *Reader) Bool() bool { return r.U1() != 0 }

// ID reads a size-byte big-endian id.
func (r *Reader) ID(size int) uint64 {
	var v uint64
	for range size {
		v = v<<8 | uint64(r.buf[r.off])
		r.off++
	}
	return v
}

func (r *Reader) String() string {
	n := int(r.U4())
	s := string(r.buf[r.off : r.off+n])
	r.off += n
	return s
}

func (r *Reader) Bytes(n int) []byte {
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b
}

func (r *Reader) Remaining() int { return len(r.buf) - r.off }
