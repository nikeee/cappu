import assert from "node:assert/strict";
import { test } from "node:test";

import { ByteReader, ByteWriter } from "./idCodec.ts";
import {
  decodePacket,
  encodeCommandPacket,
  FLAG_REPLY,
  HEADER_LEN,
  tryReadPacket,
} from "./protocol.ts";

test("ByteWriter encodes fixed-width fields big-endian", () => {
  const buf = new ByteWriter().u1(0x12).u2(0x3456).u4(0x789abcde).toBuffer();
  assert.deepEqual([...buf], [0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde]);
});

test("ByteWriter.id writes the requested width big-endian", () => {
  assert.deepEqual([...new ByteWriter().id(0x0102n, 8).toBuffer()], [0, 0, 0, 0, 0, 0, 1, 2]);
  assert.deepEqual([...new ByteWriter().id(0xabn, 4).toBuffer()], [0, 0, 0, 0xab]);
});

test("ByteWriter/ByteReader round-trip every field type", () => {
  const w = new ByteWriter()
    .u1(200)
    .u2(60000)
    .u4(0xdeadbeef)
    .i4(-5)
    .u8(0x0123456789abcdefn)
    .id(0xcafen, 8)
    .boolean(true)
    .string("héllo"); // multi-byte UTF-8 to exercise the length prefix
  const r = new ByteReader(w.toBuffer());
  assert.equal(r.u1(), 200);
  assert.equal(r.u2(), 60000);
  assert.equal(r.u4(), 0xdeadbeef);
  assert.equal(r.i4(), -5);
  assert.equal(r.u8(), 0x0123456789abcdefn);
  assert.equal(r.id(8), 0xcafen);
  assert.equal(r.boolean(), true);
  assert.equal(r.string(), "héllo");
  assert.equal(r.remaining, 0);
});

test("string length prefix counts UTF-8 bytes, not code points", () => {
  // "é" is 2 UTF-8 bytes, so a 1-char string carries a length of 2.
  const buf = new ByteWriter().string("é").toBuffer();
  assert.deepEqual([...buf.subarray(0, 4)], [0, 0, 0, 2]);
});

test("encode/decode a command packet round-trips header and body", () => {
  const data = Buffer.from([1, 2, 3]);
  const buf = encodeCommandPacket(0x2a, 1, 7, data);
  assert.equal(buf.readUInt32BE(0), HEADER_LEN + 3); // length prefix
  assert.equal(buf.readUInt8(8), 0); // command flag (not reply)
  const p = decodePacket(buf);
  assert.equal(p.kind, "command");
  assert.deepEqual(p, { kind: "command", id: 0x2a, commandSet: 1, command: 7, data });
});

test("decodePacket reads a reply (flag 0x80 + errorCode)", () => {
  const header = Buffer.alloc(HEADER_LEN);
  header.writeUInt32BE(HEADER_LEN, 0);
  header.writeUInt32BE(99, 4);
  header.writeUInt8(FLAG_REPLY, 8);
  header.writeUInt16BE(0x0015, 9); // errorCode
  const p = decodePacket(header);
  assert.equal(p.kind, "reply");
  if (p.kind === "reply") assert.equal(p.errorCode, 0x15);
  assert.equal(p.id, 99);
});

test("tryReadPacket frames by the length prefix and returns the remainder", () => {
  const a = encodeCommandPacket(1, 1, 1, Buffer.from([0xaa]));
  const b = encodeCommandPacket(2, 1, 1, Buffer.from([0xbb, 0xcc]));
  const stream = Buffer.concat([a, b]);

  // A partial buffer yields nothing.
  assert.equal(tryReadPacket(stream.subarray(0, HEADER_LEN)), null);

  const first = tryReadPacket(stream);
  assert.ok(first);
  assert.equal(first.packet.id, 1);
  assert.equal(first.rest.length, b.length);

  const second = tryReadPacket(first.rest);
  assert.ok(second);
  assert.equal(second.packet.id, 2);
  assert.equal(second.rest.length, 0);
});
