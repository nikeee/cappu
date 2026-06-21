// A JDWP client: connects to a JVM debug port, performs the handshake, and
// multiplexes synchronous command/reply exchanges with asynchronous events over
// one socket. A single data listener accumulates bytes, consumes the 14-byte
// handshake first, then frames packets by their length prefix. Replies resolve
// the pending promise keyed by packet id; Event.Composite packets go to the
// event listener.
//
// Port reference for togo/internal/jdwp/client.go.

import { connect, type Socket } from "node:net";
import { once } from "node:events";
import type { Duplex } from "node:stream";

import { ByteReader, DEFAULT_ID_SIZES, type IdSizes } from "./idCodec.ts";
import {
  CommandSet,
  EventCmd,
  encodeCommandPacket,
  HANDSHAKE,
  type Packet,
  tryReadPacket,
  VirtualMachineCmd,
} from "./protocol.ts";

/** A non-zero JDWP reply error code. */
export class JdwpError extends Error {
  constructor(readonly code: number) {
    super(`JDWP error ${code}`);
    this.name = "JdwpError";
  }
}

type Waiter = { resolve: (data: Buffer) => void; reject: (e: Error) => void };

export class JdwpClient {
  private nextId = 1;
  private readonly pending = new Map<number, Waiter>();
  private buffer: Buffer = Buffer.alloc(0);
  private handshook = false;
  private handshake?: { resolve: () => void; reject: (e: Error) => void };
  private eventListener?: (data: Buffer) => void;
  private closed = false;
  idSizes: IdSizes = DEFAULT_ID_SIZES;

  constructor(private readonly stream: Duplex) {
    stream.on("data", d => this.onData(d));
    stream.on("close", () => this.onClose(new Error("JDWP connection closed")));
    stream.on("error", e => this.onClose(e));
  }

  /** Connect over TCP, handshake, and negotiate ID sizes. */
  static async connect(host: string, port: number): Promise<JdwpClient> {
    const socket: Socket = connect({ host, port });
    await once(socket, "connect");
    return JdwpClient.attach(socket);
  }

  /** Drive the handshake + ID-size negotiation over an already-open stream. */
  static async attach(stream: Duplex): Promise<JdwpClient> {
    const client = new JdwpClient(stream);
    stream.write(Buffer.from(HANDSHAKE, "ascii"));
    await client.waitHandshake();
    await client.negotiateIdSizes();
    return client;
  }

  onEvent(listener: (data: Buffer) => void): void {
    this.eventListener = listener;
  }

  /** Send a command and resolve with its reply body (rejects on error code). */
  send(commandSet: number, command: number, data: Buffer = Buffer.alloc(0)): Promise<Buffer> {
    if (this.closed) return Promise.reject(new Error("JDWP connection closed"));
    const id = this.nextId++;
    return new Promise<Buffer>((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.stream.write(encodeCommandPacket(id, commandSet, command, data));
    });
  }

  close(): void {
    this.stream.end();
  }

  private waitHandshake(): Promise<void> {
    if (this.handshook) return Promise.resolve();
    return new Promise((resolve, reject) => (this.handshake = { resolve, reject }));
  }

  private async negotiateIdSizes(): Promise<void> {
    const data = await this.send(CommandSet.VirtualMachine, VirtualMachineCmd.IDSizes);
    const r = new ByteReader(data);
    this.idSizes = {
      fieldID: r.u4(),
      methodID: r.u4(),
      objectID: r.u4(),
      referenceTypeID: r.u4(),
      frameID: r.u4(),
    };
  }

  private onData(chunk: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    if (!this.handshook) {
      if (this.buffer.length < HANDSHAKE.length) return;
      const got = this.buffer.subarray(0, HANDSHAKE.length).toString("ascii");
      this.buffer = this.buffer.subarray(HANDSHAKE.length);
      if (got !== HANDSHAKE) {
        this.handshake?.reject(new Error(`bad JDWP handshake: ${JSON.stringify(got)}`));
        return;
      }
      this.handshook = true;
      this.handshake?.resolve();
    }
    for (;;) {
      const r = tryReadPacket(this.buffer);
      if (!r) break;
      this.buffer = r.rest;
      this.handlePacket(r.packet);
    }
  }

  private handlePacket(p: Packet): void {
    if (p.kind === "reply") {
      const waiter = this.pending.get(p.id);
      if (!waiter) return;
      this.pending.delete(p.id);
      if (p.errorCode !== 0) waiter.reject(new JdwpError(p.errorCode));
      else waiter.resolve(p.data);
      return;
    }
    if (p.commandSet === CommandSet.Event && p.command === EventCmd.Composite) {
      this.eventListener?.(p.data);
    }
  }

  private onClose(err: Error): void {
    if (this.closed) return;
    this.closed = true;
    this.handshake?.reject(err);
    for (const waiter of this.pending.values()) waiter.reject(err);
    this.pending.clear();
  }
}
