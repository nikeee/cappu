import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:net";
import { test } from "node:test";

import {
  classesBySignature,
  methodLineTable,
  methodVariableTable,
  threadFrames,
} from "./commands.ts";
import { decodeComposite } from "./events.ts";
import { ByteWriter, DEFAULT_ID_SIZES } from "./idCodec.ts";
import { JdwpClient } from "./jdwpClient.ts";
import {
  CommandSet,
  EventKind,
  FLAG_REPLY,
  HANDSHAKE,
  HEADER_LEN,
  SuspendPolicy,
  tryReadPacket,
  TypeTag,
  VirtualMachineCmd,
} from "./protocol.ts";

function reply(id: number, data: Buffer): Buffer {
  const h = Buffer.allocUnsafe(HEADER_LEN);
  h.writeUInt32BE(HEADER_LEN + data.length, 0);
  h.writeUInt32BE(id, 4);
  h.writeUInt8(FLAG_REPLY, 8);
  h.writeUInt16BE(0, 9);
  return Buffer.concat([h, data]);
}

// Fake JVM that answers each command from a (set,cmd)->body map; IDSizes auto.
async function withCannedJvm(
  responses: Record<string, Buffer>,
  fn: (client: JdwpClient) => Promise<void>,
): Promise<void> {
  const server = createServer(sock => {
    let buf: Buffer = Buffer.alloc(0);
    let handshook = false;
    sock.on("data", (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk]);
      if (!handshook) {
        if (buf.length < HANDSHAKE.length) return;
        sock.write(HANDSHAKE);
        buf = buf.subarray(HANDSHAKE.length);
        handshook = true;
      }
      for (;;) {
        const r = tryReadPacket(buf);
        if (!r) break;
        buf = r.rest;
        if (r.packet.kind !== "command") continue;
        const { id, commandSet, command } = r.packet;
        if (commandSet === CommandSet.VirtualMachine && command === VirtualMachineCmd.IDSizes) {
          const sizes = Buffer.alloc(20);
          for (let i = 0; i < 5; i++) sizes.writeUInt32BE(8, i * 4);
          sock.write(reply(id, sizes));
          continue;
        }
        sock.write(reply(id, responses[`${commandSet}:${command}`] ?? Buffer.alloc(0)));
      }
    });
  });
  server.listen(0);
  await once(server, "listening");
  const addr = server.address();
  const port = typeof addr === "object" && addr ? addr.port : 0;
  const client = await JdwpClient.connect("127.0.0.1", port);
  try {
    await fn(client);
  } finally {
    client.close();
    server.close();
  }
}

test("threadFrames decodes frameId + location pairs", async () => {
  const body = new ByteWriter()
    .u4(2)
    .id(0x11n, 8) // frame 0 id
    .u1(TypeTag.CLASS)
    .id(0xc1n, 8) // classId
    .id(0xa1n, 8) // methodId
    .u8(7n) // index
    .id(0x22n, 8) // frame 1 id
    .u1(TypeTag.CLASS)
    .id(0xc2n, 8)
    .id(0xa2n, 8)
    .u8(0n)
    .toBuffer();
  await withCannedJvm({ "11:6": body }, async client => {
    const frames = await threadFrames(client, 0xdeadn);
    assert.equal(frames.length, 2);
    assert.equal(frames[0].frameId, 0x11n);
    assert.deepEqual(frames[0].location, {
      typeTag: TypeTag.CLASS,
      classId: 0xc1n,
      methodId: 0xa1n,
      index: 7n,
    });
    assert.equal(frames[1].location.methodId, 0xa2n);
  });
});

test("methodLineTable decodes start/end and (codeIndex, line) entries", async () => {
  const body = new ByteWriter()
    .u8(0n) // start
    .u8(20n) // end
    .u4(3)
    .u8(0n)
    .i4(3) // line 3 at index 0
    .u8(5n)
    .i4(4) // line 4 at index 5
    .u8(12n)
    .i4(6) // line 6 at index 12
    .toBuffer();
  await withCannedJvm({ "6:1": body }, async client => {
    const lt = await methodLineTable(client, 0xc1n, 0xa1n);
    assert.equal(lt.end, 20n);
    assert.deepEqual(
      lt.lines.map(l => [Number(l.lineCodeIndex), l.lineNumber]),
      [
        [0, 3],
        [5, 4],
        [12, 6],
      ],
    );
  });
});

test("classesBySignature decodes class info rows", async () => {
  const body = new ByteWriter().u4(1).u1(TypeTag.CLASS).id(0xc1n, 8).i4(7).toBuffer();
  await withCannedJvm({ "1:2": body }, async client => {
    const classes = await classesBySignature(client, "Lexample/App;");
    assert.deepEqual(classes, [{ refTypeTag: TypeTag.CLASS, typeId: 0xc1n, status: 7 }]);
  });
});

test("decodeComposite reads a breakpoint sub-event", () => {
  const data = new ByteWriter()
    .u1(SuspendPolicy.EVENT_THREAD)
    .u4(1)
    .u1(EventKind.BREAKPOINT)
    .i4(42) // requestId
    .id(0x7n, 8) // thread
    .u1(TypeTag.CLASS)
    .id(0xc1n, 8) // classId
    .id(0xa1n, 8) // methodId
    .u8(5n) // index
    .toBuffer();
  const { suspendPolicy, events } = decodeComposite(data, DEFAULT_ID_SIZES);
  assert.equal(suspendPolicy, SuspendPolicy.EVENT_THREAD);
  assert.equal(events.length, 1);
  const ev = events[0];
  assert.equal(ev.kind, EventKind.BREAKPOINT);
  if (ev.kind === EventKind.BREAKPOINT) {
    assert.equal(ev.requestId, 42);
    assert.equal(ev.thread, 0x7n);
    assert.equal(ev.location.index, 5n);
  }
});

test("decodeComposite reads a class-prepare sub-event", () => {
  const data = new ByteWriter()
    .u1(SuspendPolicy.ALL)
    .u4(1)
    .u1(EventKind.CLASS_PREPARE)
    .i4(9)
    .id(0x7n, 8)
    .u1(TypeTag.CLASS)
    .id(0xc9n, 8)
    .string("Lexample/App;")
    .i4(7)
    .toBuffer();
  const { events } = decodeComposite(data, DEFAULT_ID_SIZES);
  const ev = events[0];
  assert.equal(ev.kind, EventKind.CLASS_PREPARE);
  if (ev.kind === EventKind.CLASS_PREPARE) {
    assert.equal(ev.signature, "Lexample/App;");
    assert.equal(ev.typeId, 0xc9n);
  }
});

test("decodeComposite reads a single-step sub-event", () => {
  const data = new ByteWriter()
    .u1(SuspendPolicy.ALL)
    .u4(1)
    .u1(EventKind.SINGLE_STEP)
    .i4(3)
    .id(0x2n, 8)
    .u1(TypeTag.CLASS)
    .id(0xc1n, 8)
    .id(0xa1n, 8)
    .u8(9n)
    .toBuffer();
  const ev = decodeComposite(data, DEFAULT_ID_SIZES).events[0];
  assert.equal(ev.kind, EventKind.SINGLE_STEP);
  if (ev.kind === EventKind.SINGLE_STEP) {
    assert.equal(ev.thread, 0x2n);
    assert.equal(ev.location.index, 9n);
  }
});

test("decodeComposite reads thread start/death and vm death", () => {
  const data = new ByteWriter()
    .u1(SuspendPolicy.NONE)
    .u4(3)
    .u1(EventKind.THREAD_START)
    .i4(0)
    .id(0x5n, 8)
    .u1(EventKind.THREAD_DEATH)
    .i4(0)
    .id(0x6n, 8)
    .u1(EventKind.VM_DEATH)
    .i4(0)
    .toBuffer();
  const { events } = decodeComposite(data, DEFAULT_ID_SIZES);
  assert.deepEqual(
    events.map(e => e.kind),
    [EventKind.THREAD_START, EventKind.THREAD_DEATH, EventKind.VM_DEATH],
  );
  assert.equal(events[0].kind === EventKind.THREAD_START && events[0].thread, 0x5n);
});

test("decodeComposite stops cleanly at an unknown event kind", () => {
  const data = new ByteWriter()
    .u1(SuspendPolicy.ALL)
    .u4(2)
    .u1(EventKind.BREAKPOINT)
    .i4(1)
    .id(0x1n, 8)
    .u1(TypeTag.CLASS)
    .id(0xc1n, 8)
    .id(0xa1n, 8)
    .u8(0n)
    .u1(40) // METHOD_ENTRY: a kind we do not decode -> scan stops here
    .toBuffer();
  // Only the first (decodable) event is returned; the rest cannot be skipped.
  const { events } = decodeComposite(data, DEFAULT_ID_SIZES);
  assert.equal(events.length, 1);
  assert.equal(events[0].kind, EventKind.BREAKPOINT);
});

test("methodVariableTable decodes slots, filtering happens in the caller", async () => {
  const body = new ByteWriter()
    .u4(1) // argCnt (ignored)
    .u4(2) // slot count
    .u8(0n)
    .string("args")
    .string("[Ljava/lang/String;")
    .i4(20)
    .i4(0)
    .u8(2n)
    .string("sum")
    .string("I")
    .i4(18)
    .i4(1)
    .toBuffer();
  await withCannedJvm({ "6:2": body }, async client => {
    const slots = await methodVariableTable(client, 0xc1n, 0xa1n);
    assert.equal(slots.length, 2);
    assert.deepEqual(slots[0], {
      codeIndex: 0n,
      name: "args",
      signature: "[Ljava/lang/String;",
      length: 20,
      slot: 0,
    });
    assert.equal(slots[1].name, "sum");
    assert.equal(slots[1].signature, "I");
  });
});
