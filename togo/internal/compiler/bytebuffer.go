package compiler

import "unicode/utf16"

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
