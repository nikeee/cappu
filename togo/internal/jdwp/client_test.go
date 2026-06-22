package jdwp

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

func replyPacket(id uint32, errorCode uint16, data []byte) []byte {
	out := make([]byte, HeaderLen+len(data))
	binary.BigEndian.PutUint32(out[0:], uint32(HeaderLen+len(data)))
	binary.BigEndian.PutUint32(out[4:], id)
	out[8] = FlagReply
	binary.BigEndian.PutUint16(out[9:], errorCode)
	copy(out[HeaderLen:], data)
	return out
}

func eventPacket(data []byte) []byte {
	out := make([]byte, HeaderLen+len(data))
	binary.BigEndian.PutUint32(out[0:], uint32(HeaderLen+len(data)))
	out[8] = 0
	out[9] = CSEvent
	out[10] = EVComposite
	copy(out[HeaderLen:], data)
	return out
}

// onCommand is called for every command except IDSizes (auto-answered all-8).
func fakeJVM(t *testing.T, onCommand func(conn net.Conn, id uint32, set, cmd byte, body []byte)) *Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		hs := make([]byte, len(Handshake))
		if _, err := readFull(conn, hs); err != nil {
			return
		}
		_, _ = conn.Write([]byte(Handshake))
		var buf []byte
		tmp := make([]byte, 4096)
		for {
			n, err := conn.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				for {
					p, rest, ok := TryReadPacket(buf)
					if !ok {
						break
					}
					buf = rest
					if p.IsReply {
						continue
					}
					if p.CommandSet == CSVirtualMachine && p.Command == VMIDSizes {
						sizes := make([]byte, 20)
						for i := range 5 {
							binary.BigEndian.PutUint32(sizes[i*4:], 8)
						}
						_, _ = conn.Write(replyPacket(p.ID, 0, sizes))
						continue
					}
					onCommand(conn, p.ID, p.CommandSet, p.Command, p.Data)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	client, err := Connect("127.0.0.1", port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { client.Close(); _ = ln.Close() })
	return client
}

func readFull(conn net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := conn.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func TestHandshakeAndIDSizes(t *testing.T) {
	c := fakeJVM(t, func(net.Conn, uint32, byte, byte, []byte) {})
	if c.IDSizes != (IDSizes{8, 8, 8, 8, 8}) {
		t.Fatalf("idSizes %+v", c.IDSizes)
	}
}

func TestSendReply(t *testing.T) {
	c := fakeJVM(t, func(conn net.Conn, id uint32, _, _ byte, _ []byte) {
		_, _ = conn.Write(replyPacket(id, 0, []byte("Version!")))
	})
	data, err := c.Send(CSVirtualMachine, VMVersion, nil)
	if err != nil || string(data) != "Version!" {
		t.Fatalf("got %q err %v", data, err)
	}
}

func TestSendErrorCode(t *testing.T) {
	c := fakeJVM(t, func(conn net.Conn, id uint32, _, _ byte, _ []byte) {
		_, _ = conn.Write(replyPacket(id, 0x0d, nil))
	})
	_, err := c.Send(CSThreadReference, TRName, nil)
	var je *Error
	if !errors.As(err, &je) || je.Code != 0x0d {
		t.Fatalf("want JDWP error 13, got %v", err)
	}
}

func TestEventDispatch(t *testing.T) {
	c := fakeJVM(t, func(conn net.Conn, id uint32, _, _ byte, _ []byte) {
		_, _ = conn.Write(replyPacket(id, 0, nil))
		_, _ = conn.Write(eventPacket([]byte{0xab, 0xcd}))
	})
	got := make(chan []byte, 1)
	c.OnEvent(func(b []byte) { got <- b })
	if _, err := c.Send(CSVirtualMachine, VMResume, nil); err != nil {
		t.Fatal(err)
	}
	if b := <-got; b[0] != 0xab || b[1] != 0xcd {
		t.Fatalf("event %x", b)
	}
}

func TestThreadFramesDecode(t *testing.T) {
	body := &Writer{}
	body.U4(2).
		ID(0x11, 8).U1(TypeTagClass).ID(0xc1, 8).ID(0xa1, 8).U8(7).
		ID(0x22, 8).U1(TypeTagClass).ID(0xc2, 8).ID(0xa2, 8).U8(0)
	c := fakeJVM(t, func(conn net.Conn, id uint32, set, cmd byte, _ []byte) {
		if set == CSThreadReference && cmd == TRFrames {
			_, _ = conn.Write(replyPacket(id, 0, body.Buffer()))
		} else {
			_, _ = conn.Write(replyPacket(id, 0, nil))
		}
	})
	frames, err := ThreadFrames(c, 0xdead, 0, -1)
	if err != nil || len(frames) != 2 {
		t.Fatalf("frames %v err %v", frames, err)
	}
	if frames[0].FrameID != 0x11 || frames[0].Location.MethodID != 0xa1 || frames[0].Location.Index != 7 {
		t.Fatalf("frame0 %+v", frames[0])
	}
	if frames[1].Location.MethodID != 0xa2 {
		t.Fatalf("frame1 %+v", frames[1])
	}
}

func TestLineTableDecode(t *testing.T) {
	body := &Writer{}
	body.U8(0).U8(20).U4(3).U8(0).I4(3).U8(5).I4(4).U8(12).I4(6)
	c := fakeJVM(t, func(conn net.Conn, id uint32, set, cmd byte, _ []byte) {
		_, _ = conn.Write(replyPacket(id, 0, body.Buffer()))
	})
	lt, err := MethodLineTableCmd(c, 0xc1, 0xa1)
	if err != nil || lt.End != 20 || len(lt.Lines) != 3 {
		t.Fatalf("lt %+v err %v", lt, err)
	}
	if lt.Lines[1].LineCodeIndex != 5 || lt.Lines[1].LineNumber != 4 {
		t.Fatalf("entry %+v", lt.Lines[1])
	}
}

func TestDecodeCompositeBreakpoint(t *testing.T) {
	w := &Writer{}
	w.U1(SuspendEventThread).U4(1).U1(EKBreakpoint).I4(42).ID(7, 8).
		U1(TypeTagClass).ID(0xc1, 8).ID(0xa1, 8).U8(5)
	comp := DecodeComposite(w.Buffer(), DefaultIDSizes)
	if comp.SuspendPolicy != SuspendEventThread || len(comp.Events) != 1 {
		t.Fatalf("comp %+v", comp)
	}
	ev := comp.Events[0]
	if ev.Kind != EKBreakpoint || ev.RequestID != 42 || ev.Thread != 7 || ev.Location.Index != 5 {
		t.Fatalf("event %+v", ev)
	}
}

func TestDecodeCompositeClassPrepare(t *testing.T) {
	w := &Writer{}
	w.U1(SuspendAll).U4(1).U1(EKClassPrepare).I4(9).ID(7, 8).
		U1(TypeTagClass).ID(0xc9, 8).String("Lexample/App;").I4(7)
	comp := DecodeComposite(w.Buffer(), DefaultIDSizes)
	ev := comp.Events[0]
	if ev.Kind != EKClassPrepare || ev.Signature != "Lexample/App;" || ev.TypeID != 0xc9 {
		t.Fatalf("event %+v", ev)
	}
}

func TestDecodeCompositeSingleStep(t *testing.T) {
	w := &Writer{}
	w.U1(SuspendAll).U4(1).U1(EKSingleStep).I4(3).ID(2, 8).
		U1(TypeTagClass).ID(0xc1, 8).ID(0xa1, 8).U8(9)
	ev := DecodeComposite(w.Buffer(), DefaultIDSizes).Events[0]
	if ev.Kind != EKSingleStep || ev.Thread != 2 || ev.Location.Index != 9 {
		t.Fatalf("event %+v", ev)
	}
}

func TestDecodeCompositeThreadAndVMDeath(t *testing.T) {
	w := &Writer{}
	w.U1(SuspendNone).U4(3).
		U1(EKThreadStart).I4(0).ID(5, 8).
		U1(EKThreadDeath).I4(0).ID(6, 8).
		U1(EKVMDeath).I4(0)
	comp := DecodeComposite(w.Buffer(), DefaultIDSizes)
	if len(comp.Events) != 3 {
		t.Fatalf("events %+v", comp.Events)
	}
	if comp.Events[0].Kind != EKThreadStart || comp.Events[0].Thread != 5 {
		t.Fatalf("thread start %+v", comp.Events[0])
	}
	if comp.Events[2].Kind != EKVMDeath {
		t.Fatalf("vm death %+v", comp.Events[2])
	}
}

func TestDecodeCompositeStopsAtUnknownKind(t *testing.T) {
	w := &Writer{}
	w.U1(SuspendAll).U4(2).
		U1(EKBreakpoint).I4(1).ID(1, 8).U1(TypeTagClass).ID(0xc1, 8).ID(0xa1, 8).U8(0).
		U1(40) // METHOD_ENTRY: not decoded -> scan stops, leaving only the first event
	comp := DecodeComposite(w.Buffer(), DefaultIDSizes)
	if len(comp.Events) != 1 || comp.Events[0].Kind != EKBreakpoint {
		t.Fatalf("events %+v", comp.Events)
	}
}

func TestMethodVariableTableDecode(t *testing.T) {
	body := &Writer{}
	body.U4(1). // argCnt (ignored)
			U4(2). // slot count
			U8(0).String("args").String("[Ljava/lang/String;").I4(20).I4(0).
			U8(2).String("sum").String("I").I4(18).I4(1)
	c := fakeJVM(t, func(conn net.Conn, id uint32, set, cmd byte, _ []byte) {
		if set == CSMethod && cmd == MVariableTable {
			_, _ = conn.Write(replyPacket(id, 0, body.Buffer()))
		} else {
			_, _ = conn.Write(replyPacket(id, 0, nil))
		}
	})
	slots, err := MethodVariableTable(c, 0xc1, 0xa1)
	if err != nil || len(slots) != 2 {
		t.Fatalf("slots %+v err %v", slots, err)
	}
	if slots[0] != (VariableSlot{CodeIndex: 0, Name: "args", Signature: "[Ljava/lang/String;", Length: 20, Slot: 0}) {
		t.Fatalf("slot0 %+v", slots[0])
	}
	if slots[1].Name != "sum" || slots[1].Signature != "I" {
		t.Fatalf("slot1 %+v", slots[1])
	}
}
