package jdwp

// JDWP packet framing and the command/event constants cappu's debugger uses. A
// JDWP packet is an 11-byte header followed by a command- or reply-specific
// body; all multi-byte values are big-endian. Replies set the 0x80 flag and
// carry the id of the command they answer; events arrive as Event.Composite.
// Port of src/jdwp/protocol.ts.

import "encoding/binary"

const (
	HeaderLen = 11
	FlagReply = 0x80
	Handshake = "JDWP-Handshake"
)

// Packet is a decoded JDWP packet, either a reply or a command (event).
type Packet struct {
	IsReply    bool
	ID         uint32
	CommandSet byte
	Command    byte
	ErrorCode  uint16
	Data       []byte
}

// EncodeCommandPacket frames a command packet (header + body).
func EncodeCommandPacket(id uint32, set, cmd byte, data []byte) []byte {
	out := make([]byte, HeaderLen+len(data))
	binary.BigEndian.PutUint32(out[0:], uint32(HeaderLen+len(data)))
	binary.BigEndian.PutUint32(out[4:], id)
	out[8] = 0 // flags: command packet
	out[9] = set
	out[10] = cmd
	copy(out[HeaderLen:], data)
	return out
}

// DecodePacket decodes exactly one full packet (header + body).
func DecodePacket(b []byte) Packet {
	p := Packet{
		ID:   binary.BigEndian.Uint32(b[4:]),
		Data: b[HeaderLen:],
	}
	if b[8]&FlagReply != 0 {
		p.IsReply = true
		p.ErrorCode = binary.BigEndian.Uint16(b[9:])
	} else {
		p.CommandSet = b[9]
		p.Command = b[10]
	}
	return p
}

// TryReadPacket pulls the first complete packet off a stream buffer, returning
// it plus the unconsumed remainder; ok is false when a full packet has not
// arrived yet.
func TryReadPacket(buf []byte) (packet Packet, rest []byte, ok bool) {
	if len(buf) < HeaderLen {
		return Packet{}, buf, false
	}
	length := int(binary.BigEndian.Uint32(buf[0:]))
	if len(buf) < length {
		return Packet{}, buf, false
	}
	return DecodePacket(buf[:length]), buf[length:], true
}

// Command sets.
const (
	CSVirtualMachine  byte = 1
	CSReferenceType   byte = 2
	CSClassType       byte = 3
	CSMethod          byte = 6
	CSObjectReference byte = 9
	CSStringReference byte = 10
	CSThreadReference byte = 11
	CSArrayReference  byte = 13
	CSEventRequest    byte = 15
	CSStackFrame      byte = 16
	CSEvent           byte = 64
)

// VirtualMachine commands.
const (
	VMVersion            byte = 1
	VMClassesBySignature byte = 2
	VMAllClasses         byte = 3
	VMAllThreads         byte = 4
	VMDispose            byte = 6
	VMIDSizes            byte = 7
	VMSuspend            byte = 8
	VMResume             byte = 9
	VMExit               byte = 10
	VMCreateString       byte = 11
)

// ReferenceType commands.
const (
	RTSignature  byte = 1
	RTFields     byte = 4
	RTMethods    byte = 5
	RTSourceFile byte = 7
)

// Method commands.
const (
	MLineTable     byte = 1
	MVariableTable byte = 2
)

// ObjectReference commands.
const (
	ORReferenceType byte = 1
	ORGetValues     byte = 2
	ORInvokeMethod  byte = 6
)

// StringReference commands.
const (
	SRValue byte = 1
)

// ThreadReference commands.
const (
	TRName       byte = 1
	TRSuspend    byte = 2
	TRResume     byte = 3
	TRStatus     byte = 4
	TRFrames     byte = 6
	TRFrameCount byte = 7
)

// ArrayReference commands.
const (
	ARLength    byte = 1
	ARGetValues byte = 2
)

// EventRequest commands.
const (
	ERSet   byte = 1
	ERClear byte = 2
)

// StackFrame commands.
const (
	SFGetValues byte = 1
)

// Event commands.
const (
	EVComposite byte = 100
)

// Event kinds.
const (
	EKSingleStep   byte = 1
	EKBreakpoint   byte = 2
	EKThreadStart  byte = 6
	EKThreadDeath  byte = 7
	EKClassPrepare byte = 8
	EKVMStart      byte = 90
	EKVMDeath      byte = 99
)

// Suspend policies.
const (
	SuspendNone        byte = 0
	SuspendEventThread byte = 1
	SuspendAll         byte = 2
)

// Type tags.
const (
	TypeTagClass     byte = 1
	TypeTagInterface byte = 2
	TypeTagArray     byte = 3
)

// EventRequest modifier kinds.
const (
	ModCount        byte = 1
	ModClassMatch   byte = 5
	ModLocationOnly byte = 7
	ModStep         byte = 10
)

// Step sizes and depths.
const (
	StepSizeMin   int32 = 0
	StepSizeLine  int32 = 1
	StepDepthIn   int32 = 0
	StepDepthOut  int32 = 2
	StepDepthOver int32 = 1
)

// JDWP value tag bytes.
const (
	TagArray   byte = 91  // '['
	TagByte    byte = 66  // 'B'
	TagChar    byte = 67  // 'C'
	TagObject  byte = 76  // 'L'
	TagFloat   byte = 70  // 'F'
	TagDouble  byte = 68  // 'D'
	TagInt     byte = 73  // 'I'
	TagLong    byte = 74  // 'J'
	TagShort   byte = 83  // 'S'
	TagVoid    byte = 86  // 'V'
	TagBoolean byte = 90  // 'Z'
	TagString  byte = 115 // 's'
	TagThread  byte = 116 // 't'
)
