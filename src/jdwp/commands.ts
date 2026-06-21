// Typed wrappers over the JDWP commands cappu's debugger issues. Each builds the
// command body with a ByteWriter (ID widths from the negotiated IdSizes) and
// parses the reply with a ByteReader. Locations and values have their own
// codecs because several commands embed them.
//
// Port reference for togo/internal/jdwp/commands.go.

import { ByteReader, ByteWriter, type IdSizes } from "./idCodec.ts";
import type { JdwpClient } from "./jdwpClient.ts";
import {
  CommandSet,
  EventRequestCmd,
  MethodCmd,
  ObjectReferenceCmd,
  ReferenceTypeCmd,
  StackFrameCmd,
  StringReferenceCmd,
  Tag,
  ThreadReferenceCmd,
  VirtualMachineCmd,
} from "./protocol.ts";

export interface Location {
  typeTag: number;
  classId: bigint;
  methodId: bigint;
  index: bigint;
}

export interface Frame {
  frameId: bigint;
  location: Location;
}

export interface MethodInfo {
  methodId: bigint;
  name: string;
  signature: string;
  modBits: number;
}

export interface LineTableEntry {
  lineCodeIndex: bigint;
  lineNumber: number;
}

export interface LineTable {
  start: bigint;
  end: bigint;
  lines: LineTableEntry[];
}

export interface ClassInfo {
  refTypeTag: number;
  typeId: bigint;
  status: number;
}

/** A decoded JDWP tagged value: an object reference (expandable) or a leaf. */
export type JdwpValue =
  | { tag: number; kind: "object"; objectId: bigint }
  | { tag: number; kind: "primitive"; value: number | bigint | boolean };

export function writeLocation(w: ByteWriter, sizes: IdSizes, loc: Location): void {
  w.u1(loc.typeTag)
    .id(loc.classId, sizes.referenceTypeID)
    .id(loc.methodId, sizes.methodID)
    .u8(loc.index);
}

export function readLocation(r: ByteReader, sizes: IdSizes): Location {
  return {
    typeTag: r.u1(),
    classId: r.id(sizes.referenceTypeID),
    methodId: r.id(sizes.methodID),
    index: r.u8(),
  };
}

const OBJECT_TAGS = new Set<number>([Tag.ARRAY, Tag.OBJECT, Tag.STRING, Tag.THREAD]);

/** Read a JDWP tagged value: leading tag byte then the width that tag implies. */
export function readValue(r: ByteReader, sizes: IdSizes): JdwpValue {
  const tag = r.u1();
  if (OBJECT_TAGS.has(tag)) return { tag, kind: "object", objectId: r.id(sizes.objectID) };
  switch (tag) {
    case Tag.BOOLEAN:
      return { tag, kind: "primitive", value: r.boolean() };
    case Tag.BYTE:
      return { tag, kind: "primitive", value: (r.u1() << 24) >> 24 };
    case Tag.CHAR:
      return { tag, kind: "primitive", value: r.u2() };
    case Tag.SHORT:
      return { tag, kind: "primitive", value: (r.u2() << 16) >> 16 };
    case Tag.INT:
      return { tag, kind: "primitive", value: r.i4() };
    case Tag.FLOAT:
      return { tag, kind: "primitive", value: r.bytes(4).readFloatBE(0) };
    case Tag.LONG:
      return { tag, kind: "primitive", value: BigInt.asIntN(64, r.u8()) };
    case Tag.DOUBLE:
      return { tag, kind: "primitive", value: r.bytes(8).readDoubleBE(0) };
    case Tag.VOID:
      return { tag, kind: "primitive", value: 0 };
    default:
      return { tag, kind: "object", objectId: r.id(sizes.objectID) };
  }
}

// --- VirtualMachine ---------------------------------------------------------

export function vmResume(c: JdwpClient): Promise<Buffer> {
  return c.send(CommandSet.VirtualMachine, VirtualMachineCmd.Resume);
}
export function vmSuspend(c: JdwpClient): Promise<Buffer> {
  return c.send(CommandSet.VirtualMachine, VirtualMachineCmd.Suspend);
}
export function vmExit(c: JdwpClient, code: number): Promise<Buffer> {
  return c.send(
    CommandSet.VirtualMachine,
    VirtualMachineCmd.Exit,
    new ByteWriter().i4(code).toBuffer(),
  );
}
export function vmDispose(c: JdwpClient): Promise<Buffer> {
  return c.send(CommandSet.VirtualMachine, VirtualMachineCmd.Dispose);
}

export async function allThreads(c: JdwpClient): Promise<bigint[]> {
  const r = new ByteReader(await c.send(CommandSet.VirtualMachine, VirtualMachineCmd.AllThreads));
  const n = r.u4();
  const ids: bigint[] = [];
  for (let i = 0; i < n; i++) ids.push(r.id(c.idSizes.objectID));
  return ids;
}

export async function classesBySignature(c: JdwpClient, signature: string): Promise<ClassInfo[]> {
  const body = new ByteWriter().string(signature).toBuffer();
  const r = new ByteReader(
    await c.send(CommandSet.VirtualMachine, VirtualMachineCmd.ClassesBySignature, body),
  );
  const n = r.u4();
  const out: ClassInfo[] = [];
  for (let i = 0; i < n; i++) {
    out.push({ refTypeTag: r.u1(), typeId: r.id(c.idSizes.referenceTypeID), status: r.i4() });
  }
  return out;
}

// --- ThreadReference --------------------------------------------------------

export async function threadName(c: JdwpClient, threadId: bigint): Promise<string> {
  const body = new ByteWriter().id(threadId, c.idSizes.objectID).toBuffer();
  return new ByteReader(
    await c.send(CommandSet.ThreadReference, ThreadReferenceCmd.Name, body),
  ).string();
}

export async function threadFrames(
  c: JdwpClient,
  threadId: bigint,
  start = 0,
  length = -1,
): Promise<Frame[]> {
  const body = new ByteWriter().id(threadId, c.idSizes.objectID).i4(start).i4(length).toBuffer();
  const r = new ByteReader(
    await c.send(CommandSet.ThreadReference, ThreadReferenceCmd.Frames, body),
  );
  const n = r.u4();
  const frames: Frame[] = [];
  for (let i = 0; i < n; i++) {
    frames.push({ frameId: r.id(c.idSizes.frameID), location: readLocation(r, c.idSizes) });
  }
  return frames;
}

export function threadResume(c: JdwpClient, threadId: bigint): Promise<Buffer> {
  const body = new ByteWriter().id(threadId, c.idSizes.objectID).toBuffer();
  return c.send(CommandSet.ThreadReference, ThreadReferenceCmd.Resume, body);
}

// --- ReferenceType / Method -------------------------------------------------

export async function referenceTypeSignature(c: JdwpClient, classId: bigint): Promise<string> {
  const body = new ByteWriter().id(classId, c.idSizes.referenceTypeID).toBuffer();
  return new ByteReader(
    await c.send(CommandSet.ReferenceType, ReferenceTypeCmd.Signature, body),
  ).string();
}

export async function referenceTypeMethods(c: JdwpClient, classId: bigint): Promise<MethodInfo[]> {
  const body = new ByteWriter().id(classId, c.idSizes.referenceTypeID).toBuffer();
  const r = new ByteReader(await c.send(CommandSet.ReferenceType, ReferenceTypeCmd.Methods, body));
  const n = r.u4();
  const methods: MethodInfo[] = [];
  for (let i = 0; i < n; i++) {
    methods.push({
      methodId: r.id(c.idSizes.methodID),
      name: r.string(),
      signature: r.string(),
      modBits: r.i4(),
    });
  }
  return methods;
}

export interface VariableSlot {
  codeIndex: bigint;
  name: string;
  signature: string;
  length: number;
  slot: number;
}

export async function methodVariableTable(
  c: JdwpClient,
  classId: bigint,
  methodId: bigint,
): Promise<VariableSlot[]> {
  const body = new ByteWriter()
    .id(classId, c.idSizes.referenceTypeID)
    .id(methodId, c.idSizes.methodID)
    .toBuffer();
  const r = new ByteReader(await c.send(CommandSet.Method, MethodCmd.VariableTable, body));
  r.u4(); // argCnt (unused: we filter by code-index visibility, not arg count)
  const n = r.u4();
  const slots: VariableSlot[] = [];
  for (let i = 0; i < n; i++) {
    slots.push({
      codeIndex: r.u8(),
      name: r.string(),
      signature: r.string(),
      length: r.i4(),
      slot: r.i4(),
    });
  }
  return slots;
}

export async function methodLineTable(
  c: JdwpClient,
  classId: bigint,
  methodId: bigint,
): Promise<LineTable> {
  const body = new ByteWriter()
    .id(classId, c.idSizes.referenceTypeID)
    .id(methodId, c.idSizes.methodID)
    .toBuffer();
  const r = new ByteReader(await c.send(CommandSet.Method, MethodCmd.LineTable, body));
  const start = r.u8();
  const end = r.u8();
  const n = r.u4();
  const lines: LineTableEntry[] = [];
  for (let i = 0; i < n; i++) lines.push({ lineCodeIndex: r.u8(), lineNumber: r.i4() });
  return { start, end, lines };
}

// --- EventRequest -----------------------------------------------------------

/**
 * One EventRequest.Set modifier; the wire payload depends on its kind. The
 * literal kinds match ModifierKind in protocol.ts (Count=1, ClassMatch=5,
 * LocationOnly=7, Step=10).
 */
export type Modifier =
  | { kind: 1; count: number }
  | { kind: 5; pattern: string }
  | { kind: 7; location: Location }
  | { kind: 10; threadId: bigint; size: number; depth: number };

function writeModifier(w: ByteWriter, sizes: IdSizes, m: Modifier): void {
  w.u1(m.kind);
  switch (m.kind) {
    case 1:
      w.i4(m.count);
      break;
    case 5:
      w.string(m.pattern);
      break;
    case 7:
      writeLocation(w, sizes, m.location);
      break;
    case 10:
      w.id(m.threadId, sizes.objectID).i4(m.size).i4(m.depth);
      break;
  }
}

export async function eventRequestSet(
  c: JdwpClient,
  eventKind: number,
  suspendPolicy: number,
  modifiers: Modifier[],
): Promise<number> {
  const w = new ByteWriter().u1(eventKind).u1(suspendPolicy).i4(modifiers.length);
  for (const m of modifiers) writeModifier(w, c.idSizes, m);
  const r = new ByteReader(
    await c.send(CommandSet.EventRequest, EventRequestCmd.Set, w.toBuffer()),
  );
  return r.i4();
}

export function eventRequestClear(
  c: JdwpClient,
  eventKind: number,
  requestId: number,
): Promise<Buffer> {
  const body = new ByteWriter().u1(eventKind).i4(requestId).toBuffer();
  return c.send(CommandSet.EventRequest, EventRequestCmd.Clear, body);
}

// --- StackFrame / values ----------------------------------------------------

export async function stackFrameGetValues(
  c: JdwpClient,
  threadId: bigint,
  frameId: bigint,
  slots: { slot: number; sigByte: number }[],
): Promise<JdwpValue[]> {
  const w = new ByteWriter()
    .id(threadId, c.idSizes.objectID)
    .id(frameId, c.idSizes.frameID)
    .i4(slots.length);
  for (const s of slots) w.i4(s.slot).u1(s.sigByte);
  const r = new ByteReader(
    await c.send(CommandSet.StackFrame, StackFrameCmd.GetValues, w.toBuffer()),
  );
  const n = r.u4();
  const values: JdwpValue[] = [];
  for (let i = 0; i < n; i++) values.push(readValue(r, c.idSizes));
  return values;
}

export async function stringValue(c: JdwpClient, stringId: bigint): Promise<string> {
  const body = new ByteWriter().id(stringId, c.idSizes.objectID).toBuffer();
  return new ByteReader(
    await c.send(CommandSet.StringReference, StringReferenceCmd.Value, body),
  ).string();
}

export async function objectReferenceType(
  c: JdwpClient,
  objectId: bigint,
): Promise<{ refTypeTag: number; typeId: bigint }> {
  const body = new ByteWriter().id(objectId, c.idSizes.objectID).toBuffer();
  const r = new ByteReader(
    await c.send(CommandSet.ObjectReference, ObjectReferenceCmd.ReferenceType, body),
  );
  return { refTypeTag: r.u1(), typeId: r.id(c.idSizes.referenceTypeID) };
}
