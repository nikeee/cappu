import assert from "node:assert/strict";
import { test } from "node:test";

import { signatureTagByte, signatureToType } from "./signatures.ts";

test("signatureToType renders primitives, objects and arrays", () => {
  assert.equal(signatureToType("I"), "int");
  assert.equal(signatureToType("Z"), "boolean");
  assert.equal(signatureToType("Ljava/lang/String;"), "java.lang.String");
  assert.equal(signatureToType("[I"), "int[]");
  assert.equal(signatureToType("[[Ljava/util/List;"), "java.util.List[][]");
});

test("signatureTagByte is the signature's first character", () => {
  assert.equal(signatureTagByte("I"), 73); // 'I'
  assert.equal(signatureTagByte("Ljava/lang/String;"), 76); // 'L'
  assert.equal(signatureTagByte("[I"), 91); // '['
});
