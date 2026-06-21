// Big-endian byte reader/writer for the JDWP wire protocol. JDWP sizes its
// reference IDs (objectID, methodID, referenceTypeID, fieldID, frameID) per the
// VM's VirtualMachine.IDSizes reply, so reads/writes of those take the
// negotiated width; every other field is fixed-width big-endian. IDs are kept
// as bigint to hold the full 8-byte range losslessly.
//
// Port reference for togo/internal/jdwp/idcodec.go.

export interface IdSizes {
  fieldID: number;
  methodID: number;
  objectID: number;
  referenceTypeID: number;
  frameID: number;
}

// Before VirtualMachine.IDSizes is read only fixed-width fields are decodable;
// 8 bytes is the modern HotSpot default and a safe placeholder until the real
// sizes arrive.
export const DEFAULT_ID_SIZES: IdSizes = {
  fieldID: 8,
  methodID: 8,
  objectID: 8,
  referenceTypeID: 8,
  frameID: 8,
};

export class ByteWriter {
  private readonly chunks: Buffer[] = [];

  u1(n: number): this {
    this.chunks.push(Buffer.from([n & 0xff]));
    return this;
  }
  u2(n: number): this {
    const b = Buffer.allocUnsafe(2);
    b.writeUInt16BE(n & 0xffff, 0);
    this.chunks.push(b);
    return this;
  }
  u4(n: number): this {
    const b = Buffer.allocUnsafe(4);
    b.writeUInt32BE(n >>> 0, 0);
    this.chunks.push(b);
    return this;
  }
  i4(n: number): this {
    const b = Buffer.allocUnsafe(4);
    b.writeInt32BE(n | 0, 0);
    this.chunks.push(b);
    return this;
  }
  u8(n: bigint): this {
    return this.id(n, 8);
  }
  boolean(b: boolean): this {
    return this.u1(b ? 1 : 0);
  }
  /** A `size`-byte big-endian ID (size comes from the negotiated IdSizes). */
  id(value: bigint, size: number): this {
    const b = Buffer.allocUnsafe(size);
    let v = value;
    for (let i = size - 1; i >= 0; i--) {
      b[i] = Number(v & 0xffn);
      v >>= 8n;
    }
    this.chunks.push(b);
    return this;
  }
  /** JDWP string: u4 length of the UTF-8 bytes, then the bytes (no NUL). */
  string(s: string): this {
    const bytes = Buffer.from(s, "utf8");
    this.u4(bytes.length);
    this.chunks.push(bytes);
    return this;
  }
  bytes(buf: Buffer): this {
    this.chunks.push(buf);
    return this;
  }
  get length(): number {
    return this.chunks.reduce((n, c) => n + c.length, 0);
  }
  toBuffer(): Buffer {
    return Buffer.concat(this.chunks);
  }
}

export class ByteReader {
  private offset = 0;
  constructor(private readonly buf: Buffer) {}

  u1(): number {
    return this.buf.readUInt8(this.offset++);
  }
  u2(): number {
    const v = this.buf.readUInt16BE(this.offset);
    this.offset += 2;
    return v;
  }
  u4(): number {
    const v = this.buf.readUInt32BE(this.offset);
    this.offset += 4;
    return v;
  }
  i4(): number {
    const v = this.buf.readInt32BE(this.offset);
    this.offset += 4;
    return v;
  }
  u8(): bigint {
    return this.id(8);
  }
  boolean(): boolean {
    return this.u1() !== 0;
  }
  /** Read a `size`-byte big-endian ID as a bigint. */
  id(size: number): bigint {
    let v = 0n;
    for (let i = 0; i < size; i++) v = (v << 8n) | BigInt(this.buf[this.offset++]);
    return v;
  }
  string(): string {
    const len = this.u4();
    const s = this.buf.toString("utf8", this.offset, this.offset + len);
    this.offset += len;
    return s;
  }
  bytes(n: number): Buffer {
    const b = this.buf.subarray(this.offset, this.offset + n);
    this.offset += n;
    return b;
  }
  get remaining(): number {
    return this.buf.length - this.offset;
  }
}
