package jdwp

// Decode the body of an Event.Composite packet: a suspend policy and a list of
// sub-events, each tagged by event kind with a kind-shaped payload. Only the
// kinds cappu requests are decoded; an unknown kind stops the scan (its length
// is not self-describing). Port of src/jdwp/events.ts.

// Event is one decoded JDWP sub-event. Fields are populated per Kind.
type Event struct {
	Kind       byte
	RequestID  int32
	Thread     uint64
	Location   Location
	RefTypeTag byte
	TypeID     uint64
	Signature  string
	Status     int32
}

// Composite is a decoded Event.Composite body.
type Composite struct {
	SuspendPolicy byte
	Events        []Event
}

func DecodeComposite(data []byte, s IDSizes) Composite {
	r := NewReader(data)
	comp := Composite{SuspendPolicy: r.U1()}
	count := int(r.U4())
	for range count {
		kind := r.U1()
		switch kind {
		case EKVMStart:
			comp.Events = append(comp.Events, Event{Kind: kind, RequestID: r.I4(), Thread: r.ID(s.ObjectID)})
		case EKVMDeath:
			comp.Events = append(comp.Events, Event{Kind: kind, RequestID: r.I4()})
		case EKThreadStart, EKThreadDeath:
			comp.Events = append(comp.Events, Event{Kind: kind, RequestID: r.I4(), Thread: r.ID(s.ObjectID)})
		case EKBreakpoint, EKSingleStep:
			comp.Events = append(comp.Events, Event{
				Kind:      kind,
				RequestID: r.I4(),
				Thread:    r.ID(s.ObjectID),
				Location:  readLocation(r, s),
			})
		case EKClassPrepare:
			comp.Events = append(comp.Events, Event{
				Kind:       kind,
				RequestID:  r.I4(),
				Thread:     r.ID(s.ObjectID),
				RefTypeTag: r.U1(),
				TypeID:     r.ID(s.ReferenceTypeID),
				Signature:  r.String(),
				Status:     r.I4(),
			})
		default:
			return comp // unknown kind: cannot skip, stop here
		}
	}
	return comp
}
