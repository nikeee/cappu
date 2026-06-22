// Codec tests for JDWP tagged values and locations (the pieces several commands
// embed). These are pure: they build a buffer with ByteWriter and decode it,
// exercising every value tag and ID width without a socket.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  type JdwpValue,
  type Location,
  readLocation,
  readValue,
  writeLocation,
} from "./commands.ts";
import { ByteReader, ByteWriter, type IdSizes } from "./idCodec.ts";
import { Tag, TypeTag } from "./protocol.ts";

const SIZES_8: IdSizes = { fieldID: 8, methodID: 8, objectID: 8, referenceTypeID: 8, frameID: 8 };
const SIZES_4: IdSizes = { fieldID: 4, methodID: 4, objectID: 4, referenceTypeID: 4, frameID: 4 };

function decodeValue(build: (w: ByteWriter) => void, sizes = SIZES_8): JdwpValue {
  const w = new ByteWriter();
  build(w);
  return readValue(new ByteReader(w.toBuffer()), sizes);
}

test("readValue decodes a boolean", () => {
  assert.deepEqual(decodeValue(w => w.u1(Tag.BOOLEAN).u1(1)), {
    tag: Tag.BOOLEAN,
    kind: "primitive",
    value: true,
  });
  assert.equal((decodeValue(w => w.u1(Tag.BOOLEAN).u1(0)) as { value: boolean }).value, false);
});

test("readValue sign-extends byte and short", () => {
  assert.equal((decodeValue(w => w.u1(Tag.BYTE).u1(0xff)) as { value: number }).value, -1);
  assert.equal((decodeValue(w => w.u1(Tag.BYTE).u1(0x7f)) as { value: number }).value, 127);
  assert.equal((decodeValue(w => w.u1(Tag.SHORT).u2(0xffff)) as { value: number }).value, -1);
  assert.equal((decodeValue(w => w.u1(Tag.SHORT).u2(0x8000)) as { value: number }).value, -32768);
});

test("readValue decodes char as an unsigned code unit", () => {
  assert.equal((decodeValue(w => w.u1(Tag.CHAR).u2(0x0041)) as { value: number }).value, 65);
});

test("readValue decodes int including negatives", () => {
  assert.equal((decodeValue(w => w.u1(Tag.INT).i4(-2147483648)) as { value: number }).value, -2147483648);
  assert.equal((decodeValue(w => w.u1(Tag.INT).i4(123456)) as { value: number }).value, 123456);
});

test("readValue decodes a signed 64-bit long", () => {
  assert.equal((decodeValue(w => w.u1(Tag.LONG).u8(0xffffffffffffffffn)) as { value: bigint }).value, -1n);
  assert.equal(
    (decodeValue(w => w.u1(Tag.LONG).u8(0x7fffffffffffffffn)) as { value: bigint }).value,
    9223372036854775807n,
  );
});

test("readValue decodes float and double", () => {
  const f = Buffer.allocUnsafe(4);
  f.writeFloatBE(1.5, 0);
  assert.equal((decodeValue(w => w.u1(Tag.FLOAT).bytes(f)) as { value: number }).value, 1.5);
  const d = Buffer.allocUnsafe(8);
  d.writeDoubleBE(-2.25, 0);
  assert.equal((decodeValue(w => w.u1(Tag.DOUBLE).bytes(d)) as { value: number }).value, -2.25);
});

test("readValue decodes object/string/array/thread tags as object refs", () => {
  for (const tag of [Tag.OBJECT, Tag.STRING, Tag.ARRAY, Tag.THREAD]) {
    const v = decodeValue(w => w.u1(tag).id(0xabcn, 8));
    assert.deepEqual(v, { tag, kind: "object", objectId: 0xabcn });
  }
});

test("readValue reads a null object reference as objectId 0", () => {
  const v = decodeValue(w => w.u1(Tag.OBJECT).id(0n, 8));
  assert.equal(v.kind, "object");
  assert.equal(v.kind === "object" && v.objectId, 0n);
});

test("readValue honours non-default (4-byte) object id width", () => {
  const v = decodeValue(w => w.u1(Tag.OBJECT).id(0xdeadn, 4), SIZES_4);
  assert.equal(v.kind === "object" && v.objectId, 0xdeadn);
});

test("writeLocation/readLocation round-trip at 8- and 4-byte widths", () => {
  const loc: Location = { typeTag: TypeTag.CLASS, classId: 0xc1n, methodId: 0xa1n, index: 42n };
  for (const sizes of [SIZES_8, SIZES_4]) {
    const w = new ByteWriter();
    writeLocation(w, sizes, loc);
    assert.deepEqual(readLocation(new ByteReader(w.toBuffer()), sizes), loc);
  }
});
