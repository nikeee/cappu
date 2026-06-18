import { test } from "node:test";

import { expect } from "expect";

import { readZipEntries } from "./zipReader.ts";
import { writeZip } from "./zipWriter.ts";

test("written archives read back through our own zip reader", () => {
  const entries = [
    {
      name: "META-INF/MANIFEST.MF",
      bytes: new TextEncoder().encode("Manifest-Version: 1.0\r\n\r\n"),
    },
    { name: "com/app/Foo.class", bytes: new Uint8Array([0xca, 0xfe, 0xba, 0xbe, 1, 2, 3]) },
    { name: "empty.txt", bytes: new Uint8Array(0) },
  ];
  const zip = writeZip(entries);
  const read = readZipEntries(zip);
  expect(read?.map(e => e.name)).toEqual(entries.map(e => e.name));
  expect([...read![1]!.read()]).toEqual([...entries[1]!.bytes]);
  expect(read![2]!.read()).toHaveLength(0);
});
