// Minimal zip reading for .jar classpath entries: end-of-central-directory,
// central directory walk, stored/deflated entry extraction via node:zlib. No
// zip64 (a jar that large is out of scope) and no encryption.

import { inflateRawSync } from "node:zlib";

const utf8 = new TextDecoder();

const EOCD_SIGNATURE = 0x06054b50;
const CENTRAL_SIGNATURE = 0x02014b50;
const EOCD_MIN_SIZE = 22;
const MAX_COMMENT = 0xffff;

export interface ZipEntry {
  readonly name: string;
  /** Decompressed bytes (lazily extracted). */
  read(): Uint8Array;
}

/** The entries of a zip archive, or undefined when it is not a zip at all. */
export function readZipEntries(bytes: Uint8Array): ZipEntry[] | undefined {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  // The end-of-central-directory record sits at the end, before a variable
  // length comment: scan backwards for its signature.
  let eocd = -1;
  const scanFrom = Math.max(0, bytes.length - EOCD_MIN_SIZE - MAX_COMMENT);
  for (let i = bytes.length - EOCD_MIN_SIZE; i >= scanFrom; i--) {
    if (view.getUint32(i, true) === EOCD_SIGNATURE) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) return undefined;

  const entryCount = view.getUint16(eocd + 10, true);
  let at = view.getUint32(eocd + 16, true); // central directory offset
  const entries: ZipEntry[] = [];
  for (let i = 0; i < entryCount; i++) {
    if (view.getUint32(at, true) !== CENTRAL_SIGNATURE) break;
    const method = view.getUint16(at + 10, true);
    const compressedSize = view.getUint32(at + 20, true);
    const nameLength = view.getUint16(at + 28, true);
    const extraLength = view.getUint16(at + 30, true);
    const commentLength = view.getUint16(at + 32, true);
    const localOffset = view.getUint32(at + 42, true);
    const name = utf8.decode(bytes.subarray(at + 46, at + 46 + nameLength));
    entries.push({
      name,
      read() {
        // The local header's name/extra lengths can differ from the central
        // directory's (the extra field often does), so re-read them.
        const localName = view.getUint16(localOffset + 26, true);
        const localExtra = view.getUint16(localOffset + 28, true);
        const start = localOffset + 30 + localName + localExtra;
        const raw = bytes.subarray(start, start + compressedSize);
        return method === 8 ? new Uint8Array(inflateRawSync(raw)) : raw;
      },
    });
    at += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}
