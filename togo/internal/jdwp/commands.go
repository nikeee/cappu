package jdwp

// Typed wrappers over the JDWP commands cappu's debugger issues. Each builds the
// command body with a Writer (ID widths from the negotiated IDSizes) and parses
// the reply with a Reader. Locations and values have their own codecs because
// several commands embed them. Port of src/jdwp/commands.ts.

import "math"

type Location struct {
	TypeTag  byte
	ClassID  uint64
	MethodID uint64
	Index    uint64
}

type Frame struct {
	FrameID  uint64
	Location Location
}

type MethodInfo struct {
	MethodID  uint64
	Name      string
	Signature string
	ModBits   int32
}

type LineTableEntry struct {
	LineCodeIndex uint64
	LineNumber    int32
}

type LineTable struct {
	Start uint64
	End   uint64
	Lines []LineTableEntry
}

type ClassInfo struct {
	RefTypeTag byte
	TypeID     uint64
	Status     int32
}

type VariableSlot struct {
	CodeIndex uint64
	Name      string
	Signature string
	Length    int32
	Slot      int32
}

// Value is a decoded JDWP tagged value. Object is true for reference types
// (the ObjectID is then meaningful); otherwise the primitive payload is in the
// field matching Tag.
type Value struct {
	Tag      byte
	Object   bool
	ObjectID uint64
	Int      int64
	Float    float64
	Bool     bool
}

func writeLocation(w *Writer, s IDSizes, loc Location) {
	w.U1(loc.TypeTag).ID(loc.ClassID, s.ReferenceTypeID).ID(loc.MethodID, s.MethodID).U8(loc.Index)
}

func readLocation(r *Reader, s IDSizes) Location {
	return Location{
		TypeTag:  r.U1(),
		ClassID:  r.ID(s.ReferenceTypeID),
		MethodID: r.ID(s.MethodID),
		Index:    r.U8(),
	}
}

func isObjectTag(tag byte) bool {
	switch tag {
	case TagArray, TagObject, TagString, TagThread:
		return true
	}
	return false
}

// ReadValue reads a JDWP tagged value: leading tag byte then the implied width.
func ReadValue(r *Reader, s IDSizes) Value {
	tag := r.U1()
	if isObjectTag(tag) {
		return Value{Tag: tag, Object: true, ObjectID: r.ID(s.ObjectID)}
	}
	switch tag {
	case TagBoolean:
		return Value{Tag: tag, Bool: r.Bool()}
	case TagByte:
		return Value{Tag: tag, Int: int64(int8(r.U1()))}
	case TagChar:
		return Value{Tag: tag, Int: int64(r.U2())}
	case TagShort:
		return Value{Tag: tag, Int: int64(int16(r.U2()))}
	case TagInt:
		return Value{Tag: tag, Int: int64(r.I4())}
	case TagLong:
		return Value{Tag: tag, Int: int64(r.U8())}
	case TagFloat:
		return Value{Tag: tag, Float: float64(math.Float32frombits(r.U4()))}
	case TagDouble:
		return Value{Tag: tag, Float: math.Float64frombits(r.U8())}
	case TagVoid:
		return Value{Tag: tag}
	default:
		return Value{Tag: tag, Object: true, ObjectID: r.ID(s.ObjectID)}
	}
}

// --- VirtualMachine ---------------------------------------------------------

func VMResumeCmd(c *Client) error  { _, err := c.Send(CSVirtualMachine, VMResume, nil); return err }
func VMSuspendCmd(c *Client) error { _, err := c.Send(CSVirtualMachine, VMSuspend, nil); return err }
func VMDisposeCmd(c *Client) error { _, err := c.Send(CSVirtualMachine, VMDispose, nil); return err }

func VMExitCmd(c *Client, code int32) error {
	w := &Writer{}
	w.I4(code)
	_, err := c.Send(CSVirtualMachine, VMExit, w.Buffer())
	return err
}

func AllThreads(c *Client) ([]uint64, error) {
	data, err := c.Send(CSVirtualMachine, VMAllThreads, nil)
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	n := int(r.U4())
	ids := make([]uint64, n)
	for i := range n {
		ids[i] = r.ID(c.IDSizes.ObjectID)
	}
	return ids, nil
}

func ClassesBySignature(c *Client, signature string) ([]ClassInfo, error) {
	w := &Writer{}
	w.String(signature)
	data, err := c.Send(CSVirtualMachine, VMClassesBySignature, w.Buffer())
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	n := int(r.U4())
	out := make([]ClassInfo, n)
	for i := range n {
		out[i] = ClassInfo{RefTypeTag: r.U1(), TypeID: r.ID(c.IDSizes.ReferenceTypeID), Status: r.I4()}
	}
	return out, nil
}

// --- ThreadReference --------------------------------------------------------

func ThreadName(c *Client, threadID uint64) (string, error) {
	w := &Writer{}
	w.ID(threadID, c.IDSizes.ObjectID)
	data, err := c.Send(CSThreadReference, TRName, w.Buffer())
	if err != nil {
		return "", err
	}
	return NewReader(data).String(), nil
}

func ThreadFrames(c *Client, threadID uint64, start, length int32) ([]Frame, error) {
	w := &Writer{}
	w.ID(threadID, c.IDSizes.ObjectID).I4(start).I4(length)
	data, err := c.Send(CSThreadReference, TRFrames, w.Buffer())
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	n := int(r.U4())
	frames := make([]Frame, n)
	for i := range n {
		frames[i] = Frame{FrameID: r.ID(c.IDSizes.FrameID), Location: readLocation(r, c.IDSizes)}
	}
	return frames, nil
}

// --- ReferenceType / Method -------------------------------------------------

func ReferenceTypeSignature(c *Client, classID uint64) (string, error) {
	w := &Writer{}
	w.ID(classID, c.IDSizes.ReferenceTypeID)
	data, err := c.Send(CSReferenceType, RTSignature, w.Buffer())
	if err != nil {
		return "", err
	}
	return NewReader(data).String(), nil
}

func ReferenceTypeMethods(c *Client, classID uint64) ([]MethodInfo, error) {
	w := &Writer{}
	w.ID(classID, c.IDSizes.ReferenceTypeID)
	data, err := c.Send(CSReferenceType, RTMethods, w.Buffer())
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	n := int(r.U4())
	methods := make([]MethodInfo, n)
	for i := range n {
		methods[i] = MethodInfo{
			MethodID:  r.ID(c.IDSizes.MethodID),
			Name:      r.String(),
			Signature: r.String(),
			ModBits:   r.I4(),
		}
	}
	return methods, nil
}

func MethodVariableTable(c *Client, classID, methodID uint64) ([]VariableSlot, error) {
	w := &Writer{}
	w.ID(classID, c.IDSizes.ReferenceTypeID).ID(methodID, c.IDSizes.MethodID)
	data, err := c.Send(CSMethod, MVariableTable, w.Buffer())
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	r.U4() // argCnt (unused: visibility is by code-index range, not arg count)
	n := int(r.U4())
	slots := make([]VariableSlot, n)
	for i := range n {
		slots[i] = VariableSlot{
			CodeIndex: r.U8(),
			Name:      r.String(),
			Signature: r.String(),
			Length:    r.I4(),
			Slot:      r.I4(),
		}
	}
	return slots, nil
}

func MethodLineTableCmd(c *Client, classID, methodID uint64) (LineTable, error) {
	w := &Writer{}
	w.ID(classID, c.IDSizes.ReferenceTypeID).ID(methodID, c.IDSizes.MethodID)
	data, err := c.Send(CSMethod, MLineTable, w.Buffer())
	if err != nil {
		return LineTable{}, err
	}
	r := NewReader(data)
	lt := LineTable{Start: r.U8(), End: r.U8()}
	n := int(r.U4())
	lt.Lines = make([]LineTableEntry, n)
	for i := range n {
		lt.Lines[i] = LineTableEntry{LineCodeIndex: r.U8(), LineNumber: r.I4()}
	}
	return lt, nil
}

// --- EventRequest -----------------------------------------------------------

// Modifier is one EventRequest.Set modifier; the wire payload depends on Kind
// (Count, ClassMatch, LocationOnly or Step).
type Modifier struct {
	Kind      byte
	Count     int32
	Pattern   string
	Location  Location
	ThreadID  uint64
	StepSize  int32
	StepDepth int32
}

func writeModifier(w *Writer, s IDSizes, m Modifier) {
	w.U1(m.Kind)
	switch m.Kind {
	case ModCount:
		w.I4(m.Count)
	case ModClassMatch:
		w.String(m.Pattern)
	case ModLocationOnly:
		writeLocation(w, s, m.Location)
	case ModStep:
		w.ID(m.ThreadID, s.ObjectID).I4(m.StepSize).I4(m.StepDepth)
	}
}

func EventRequestSet(c *Client, eventKind, suspendPolicy byte, modifiers []Modifier) (int32, error) {
	w := &Writer{}
	w.U1(eventKind).U1(suspendPolicy).I4(int32(len(modifiers)))
	for _, m := range modifiers {
		writeModifier(w, c.IDSizes, m)
	}
	data, err := c.Send(CSEventRequest, ERSet, w.Buffer())
	if err != nil {
		return 0, err
	}
	return NewReader(data).I4(), nil
}

func EventRequestClear(c *Client, eventKind byte, requestID int32) error {
	w := &Writer{}
	w.U1(eventKind).I4(requestID)
	_, err := c.Send(CSEventRequest, ERClear, w.Buffer())
	return err
}

// --- StackFrame / values ----------------------------------------------------

type Slot struct {
	Slot    int32
	SigByte byte
}

func StackFrameGetValues(c *Client, threadID, frameID uint64, slots []Slot) ([]Value, error) {
	w := &Writer{}
	w.ID(threadID, c.IDSizes.ObjectID).ID(frameID, c.IDSizes.FrameID).I4(int32(len(slots)))
	for _, s := range slots {
		w.I4(s.Slot).U1(s.SigByte)
	}
	data, err := c.Send(CSStackFrame, SFGetValues, w.Buffer())
	if err != nil {
		return nil, err
	}
	r := NewReader(data)
	n := int(r.U4())
	values := make([]Value, n)
	for i := range n {
		values[i] = ReadValue(r, c.IDSizes)
	}
	return values, nil
}

func StringValue(c *Client, stringID uint64) (string, error) {
	w := &Writer{}
	w.ID(stringID, c.IDSizes.ObjectID)
	data, err := c.Send(CSStringReference, SRValue, w.Buffer())
	if err != nil {
		return "", err
	}
	return NewReader(data).String(), nil
}
