// The Debug Adapter Protocol wire transport: the same `Content-Length`-framed
// stream LSP uses, but with DAP's envelope (a monotonic `seq`, a `type` of
// request/response/event) instead of JSON-RPC. Hand-rolled to match the LSP
// side (src/services/server.ts) so the Go port stays byte-comparable, rather
// than pulling in @vscode/debugadapter.
//
// Port reference for togo/internal/dap/conn.go.

import type { Readable, Writable } from "node:stream";

export interface DapRequest {
  seq: number;
  type: "request";
  command: string;
  arguments?: unknown;
}

interface DapResponse {
  seq: number;
  type: "response";
  request_seq: number;
  success: boolean;
  command: string;
  message?: string;
  body?: unknown;
}

interface DapEvent {
  seq: number;
  type: "event";
  event: string;
  body?: unknown;
}

export type RequestHandler = (args: unknown, request: DapRequest) => unknown | Promise<unknown>;

export class DapConnection {
  private seq = 1;
  private buffer: Buffer = Buffer.alloc(0);
  private readonly handlers = new Map<string, RequestHandler>();
  private onClose?: () => void;

  constructor(
    private readonly reader: Readable,
    private readonly writer: Writable,
  ) {}

  onRequest(command: string, handler: RequestHandler): void {
    this.handlers.set(command, handler);
  }

  /** Push a DAP event (stopped, output, terminated, ...) to the client. */
  sendEvent(event: string, body?: unknown): void {
    this.write({ seq: this.seq++, type: "event", event, body });
  }

  /** Start the read loop; resolves when the input stream ends. */
  run(onClose?: () => void): Promise<void> {
    this.onClose = onClose;
    return new Promise(resolve => {
      this.reader.on("data", (chunk: Buffer) => this.onData(chunk));
      this.reader.on("end", () => {
        this.onClose?.();
        resolve();
      });
      this.reader.on("close", () => {
        this.onClose?.();
        resolve();
      });
    });
  }

  private onData(chunk: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const message = this.tryRead();
      if (!message) break;
      this.dispatch(message);
    }
  }

  // Pull one complete Content-Length-framed message off the buffer.
  private tryRead(): DapRequest | null {
    const sep = this.buffer.indexOf("\r\n\r\n");
    if (sep < 0) return null;
    const header = this.buffer.toString("ascii", 0, sep);
    const match = /Content-Length:\s*(\d+)/i.exec(header);
    if (!match) {
      // Unrecoverable framing: drop the bad header and resync.
      this.buffer = this.buffer.subarray(sep + 4);
      return null;
    }
    const length = Number(match[1]);
    const start = sep + 4;
    if (this.buffer.length < start + length) return null;
    const body = this.buffer.toString("utf8", start, start + length);
    this.buffer = this.buffer.subarray(start + length);
    try {
      return JSON.parse(body) as DapRequest;
    } catch {
      return null;
    }
  }

  private async dispatch(message: DapRequest): Promise<void> {
    if (message.type !== "request") return; // adapters never receive responses
    const handler = this.handlers.get(message.command);
    if (!handler) {
      this.respond(message, false, undefined, `unsupported request '${message.command}'`);
      return;
    }
    try {
      const body = await handler(message.arguments, message);
      this.respond(message, true, body);
    } catch (e) {
      this.respond(message, false, undefined, (e as Error).message);
    }
  }

  private respond(req: DapRequest, success: boolean, body?: unknown, message?: string): void {
    this.write({
      seq: this.seq++,
      type: "response",
      request_seq: req.seq,
      success,
      command: req.command,
      message,
      body,
    });
  }

  private write(msg: DapResponse | DapEvent): void {
    const body = Buffer.from(JSON.stringify(msg), "utf8");
    this.writer.write(`Content-Length: ${body.length}\r\n\r\n`);
    this.writer.write(body);
  }
}
