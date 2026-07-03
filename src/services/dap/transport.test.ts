import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";

import { DapConnection } from "./transport.ts";

function frame(msg: unknown): string {
  const body = JSON.stringify(msg);
  return `Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`;
}

// Collect framed messages emitted on a stream, resolving once `count` arrive.
function collect(stream: PassThrough, count: number): Promise<any[]> {
  return new Promise(resolve => {
    let buf = "";
    const out: any[] = [];
    stream.on("data", (c: Buffer) => {
      buf += c.toString("utf8");
      for (;;) {
        const sep = buf.indexOf("\r\n\r\n");
        if (sep < 0) break;
        const len = Number(/Content-Length:\s*(\d+)/i.exec(buf.slice(0, sep))![1]);
        if (buf.length < sep + 4 + len) break;
        out.push(JSON.parse(buf.slice(sep + 4, sep + 4 + len)));
        buf = buf.slice(sep + 4 + len);
        if (out.length === count) resolve(out);
      }
    });
  });
}

test("a request is dispatched and answered with a response carrying request_seq", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  conn.onRequest("initialize", () => ({ supportsConfigurationDoneRequest: true }));
  void conn.run();

  const responses = collect(output, 1);
  input.write(frame({ seq: 7, type: "request", command: "initialize" }));

  const [resp] = await responses;
  assert.equal(resp.type, "response");
  assert.equal(resp.command, "initialize");
  assert.equal(resp.request_seq, 7);
  assert.equal(resp.success, true);
  assert.deepEqual(resp.body, { supportsConfigurationDoneRequest: true });
});

test("an unknown request yields success:false with a message", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  void conn.run();

  const responses = collect(output, 1);
  input.write(frame({ seq: 1, type: "request", command: "nope" }));

  const [resp] = await responses;
  assert.equal(resp.success, false);
  assert.match(resp.message, /unsupported request 'nope'/);
});

test("a throwing handler reports success:false with the error message", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  conn.onRequest("launch", () => {
    throw new Error("no main class");
  });
  void conn.run();

  const responses = collect(output, 1);
  input.write(frame({ seq: 2, type: "request", command: "launch" }));

  const [resp] = await responses;
  assert.equal(resp.success, false);
  assert.equal(resp.message, "no main class");
});

test("sendEvent emits an event frame; seq increases monotonically", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  void conn.run();

  const messages = collect(output, 2);
  conn.sendEvent("initialized");
  conn.sendEvent("stopped", { reason: "pause", threadId: 1 });

  const [a, b] = await messages;
  assert.equal(a.type, "event");
  assert.equal(a.event, "initialized");
  assert.deepEqual(b.body, { reason: "pause", threadId: 1 });
  assert.ok(b.seq > a.seq);
});

test("two requests arriving in one chunk are both dispatched", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  conn.onRequest("ping", (args: any) => ({ n: args.n }));
  void conn.run();

  const responses = collect(output, 2);
  input.write(
    frame({ seq: 1, type: "request", command: "ping", arguments: { n: 1 } }) +
      frame({ seq: 2, type: "request", command: "ping", arguments: { n: 2 } }),
  );

  const [r1, r2] = await responses;
  assert.deepEqual([r1.body.n, r2.body.n], [1, 2]);
});

test("a request split across two chunks is reassembled and dispatched", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  conn.onRequest("ping", (args: any) => ({ n: args.n }));
  void conn.run();

  const responses = collect(output, 1);
  const whole = frame({ seq: 1, type: "request", command: "ping", arguments: { n: 9 } });
  const split = Math.floor(whole.length / 2);
  input.write(whole.slice(0, split)); // header + part of the body
  await sleep(5);
  input.write(whole.slice(split)); // the remainder completes the frame

  const [resp] = await responses;
  assert.equal(resp.success, true);
  assert.equal(resp.body.n, 9);
});

test("an event with no body omits the body field", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  void conn.run();

  const messages = collect(output, 1);
  conn.sendEvent("initialized");
  const [a] = await messages;
  assert.equal(a.event, "initialized");
  assert.equal("body" in a, false);
});

test("a malformed frame in a chunk does not stall the valid request behind it", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  const conn = new DapConnection(input, output);
  conn.onRequest("ping", (args: any) => ({ n: args.n }));
  void conn.run();

  const responses = collect(output, 1);
  const bad = "Content-Length: 8\r\n\r\nnot-json";
  const noLength = "X-Other: 1\r\n\r\n";
  input.write(
    bad + noLength + frame({ seq: 3, type: "request", command: "ping", arguments: { n: 5 } }),
  );

  const [resp] = await responses;
  assert.equal(resp.success, true);
  assert.equal(resp.body.n, 5);
});
