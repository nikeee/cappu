// JDWP packet framing and the command/event constants cappu's debugger uses.
// A JDWP packet is an 11-byte header followed by a command-set-specific body:
//   length:u4  id:u4  flags:u1  then (commandSet:u1 command:u1) for a command
//                                 or (errorCode:u2)              for a reply.
// All multi-byte values are big-endian. Replies set the 0x80 flag bit and carry
// the id of the command they answer; events arrive as Event.Composite commands.
//
// Port reference for togo/internal/jdwp/protocol.go.

export const HEADER_LEN = 11;
export const FLAG_REPLY = 0x80;
export const HANDSHAKE = "JDWP-Handshake";

export type Packet =
  | { kind: "command"; id: number; commandSet: number; command: number; data: Buffer }
  | { kind: "reply"; id: number; errorCode: number; data: Buffer };

export function encodeCommandPacket(
  id: number,
  commandSet: number,
  command: number,
  data: Buffer,
): Buffer {
  const header = Buffer.allocUnsafe(HEADER_LEN);
  header.writeUInt32BE(HEADER_LEN + data.length, 0);
  header.writeUInt32BE(id >>> 0, 4);
  header.writeUInt8(0, 8); // flags: command packet
  header.writeUInt8(commandSet, 9);
  header.writeUInt8(command, 10);
  return Buffer.concat([header, data]);
}

/** Decode exactly one full packet (header + body); caller frames by length. */
export function decodePacket(buf: Buffer): Packet {
  const id = buf.readUInt32BE(4);
  const flags = buf.readUInt8(8);
  const data = buf.subarray(HEADER_LEN);
  if (flags & FLAG_REPLY) {
    return { kind: "reply", id, errorCode: buf.readUInt16BE(9), data };
  }
  return {
    kind: "command",
    id,
    commandSet: buf.readUInt8(9),
    command: buf.readUInt8(10),
    data,
  };
}

/**
 * Pull the first complete packet off a stream buffer. Returns the decoded
 * packet plus the unconsumed remainder, or null when fewer than a full packet's
 * bytes have arrived yet (the length prefix tells us how many we need).
 */
export function tryReadPacket(buf: Buffer): { packet: Packet; rest: Buffer } | null {
  if (buf.length < HEADER_LEN) return null;
  const length = buf.readUInt32BE(0);
  if (buf.length < length) return null;
  return { packet: decodePacket(buf.subarray(0, length)), rest: buf.subarray(length) };
}

// Command sets and the commands within them that cappu issues. Numbers are from
// the JDWP spec (java.se/docs jdwp-protocol). Only the subset the debugger uses
// is listed.
export const CommandSet = {
  VirtualMachine: 1,
  ReferenceType: 2,
  ClassType: 3,
  Method: 6,
  ObjectReference: 9,
  StringReference: 10,
  ThreadReference: 11,
  ArrayReference: 13,
  EventRequest: 15,
  StackFrame: 16,
  Event: 64,
} as const;

export const VirtualMachineCmd = {
  Version: 1,
  ClassesBySignature: 2,
  AllClasses: 3,
  AllThreads: 4,
  Dispose: 6,
  IDSizes: 7,
  Suspend: 8,
  Resume: 9,
  Exit: 10,
  CreateString: 11,
} as const;

export const ReferenceTypeCmd = {
  Signature: 1,
  Fields: 4,
  Methods: 5,
  SourceFile: 7,
} as const;

export const MethodCmd = {
  LineTable: 1,
  VariableTable: 2,
  VariableTableWithGeneric: 5,
} as const;

export const ObjectReferenceCmd = {
  ReferenceType: 1,
  GetValues: 2,
  InvokeMethod: 6,
} as const;

export const StringReferenceCmd = { Value: 1 } as const;

export const ThreadReferenceCmd = {
  Name: 1,
  Suspend: 2,
  Resume: 3,
  Status: 4,
  Frames: 6,
  FrameCount: 7,
} as const;

export const ArrayReferenceCmd = { Length: 1, GetValues: 2 } as const;

export const EventRequestCmd = { Set: 1, Clear: 2 } as const;

export const StackFrameCmd = { GetValues: 1 } as const;

export const EventCmd = { Composite: 100 } as const;

// JDWP enums used when building event requests and decoding events/values.
export const EventKind = {
  SINGLE_STEP: 1,
  BREAKPOINT: 2,
  THREAD_START: 6,
  THREAD_DEATH: 7,
  CLASS_PREPARE: 8,
  VM_START: 90,
  VM_DEATH: 99,
} as const;

export const SuspendPolicy = { NONE: 0, EVENT_THREAD: 1, ALL: 2 } as const;

export const TypeTag = { CLASS: 1, INTERFACE: 2, ARRAY: 3 } as const;

export const ModifierKind = {
  Count: 1,
  ClassMatch: 5,
  LocationOnly: 7,
  Step: 10,
} as const;

export const StepSize = { MIN: 0, LINE: 1 } as const;
export const StepDepth = { INTO: 0, OVER: 1, OUT: 2 } as const;

// Value tag bytes (JDWP Value): the leading tag of a tagged value tells you how
// to decode the rest. Only the subset the variable formatter handles in v1.
export const Tag = {
  ARRAY: 91, // '['
  BYTE: 66, // 'B'
  CHAR: 67, // 'C'
  OBJECT: 76, // 'L'
  FLOAT: 70, // 'F'
  DOUBLE: 68, // 'D'
  INT: 73, // 'I'
  LONG: 74, // 'J'
  SHORT: 83, // 'S'
  VOID: 86, // 'V'
  BOOLEAN: 90, // 'Z'
  STRING: 115, // 's'
  THREAD: 116, // 't'
} as const;
