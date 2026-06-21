import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type Server, type Socket } from "node:net";
import { test } from "node:test";

import { JdwpClient, JdwpError } from "./jdwpClient.ts";
import {
  CommandSet,
  EventCmd,
  FLAG_REPLY,
  HANDSHAKE,
  HEADER_LEN,
  tryReadPacket,
  VirtualMachineCmd,
} from "./protocol.ts";

function reply(id: number, errorCode: number, data: Buffer): Buffer {
  const h = Buffer.allocUnsafe(HEADER_LEN);
  h.writeUInt32BE(HEADER_LEN + data.length, 0);
  h.writeUInt32BE(id, 4);
  h.writeUInt8(FLAG_REPLY, 8);
  h.writeUInt16BE(errorCode, 9);
  return Buffer.concat([h, data]);
}

function event(data: Buffer): Buffer {
  const h = Buffer.allocUnsafe(HEADER_LEN);
  h.writeUInt32BE(HEADER_LEN + data.length, 0);
  h.writeUInt32BE(0, 4); // events carry id 0
  h.writeUInt8(0, 8);
  h.writeUInt8(CommandSet.Event, 9);
  h.writeUInt8(EventCmd.Composite, 10);
  return Buffer.concat([h, data]);
}

// A loopback JDWP server: echoes the handshake, auto-answers IDSizes (all 8),
// and lets the test react to every other command.
function fakeJvm(
  onCommand: (sock: Socket, id: number, set: number, cmd: number, body: Buffer) => void,
) {
  const server = createServer(sock => {
    let buf: Buffer = Buffer.alloc(0);
    let handshook = false;
    sock.on("data", (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk]);
      if (!handshook) {
        if (buf.length < HANDSHAKE.length) return;
        sock.write(HANDSHAKE); // echo the handshake back
        buf = buf.subarray(HANDSHAKE.length);
        handshook = true;
      }
      for (;;) {
        const r = tryReadPacket(buf);
        if (!r) break;
        buf = r.rest;
        if (r.packet.kind !== "command") continue;
        const { id, commandSet, command, data } = r.packet;
        if (commandSet === CommandSet.VirtualMachine && command === VirtualMachineCmd.IDSizes) {
          const sizes = Buffer.alloc(20);
          for (let i = 0; i < 5; i++) sizes.writeUInt32BE(8, i * 4);
          sock.write(reply(id, 0, sizes));
        } else {
          onCommand(sock, id, commandSet, command, data);
        }
      }
    });
  });
  return server;
}

async function withServer(
  server: Server,
  fn: (client: JdwpClient) => Promise<void>,
): Promise<void> {
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

test("handshake + IDSizes negotiation", async () => {
  await withServer(
    fakeJvm(() => {}),
    async client => {
      assert.deepEqual(client.idSizes, {
        fieldID: 8,
        methodID: 8,
        objectID: 8,
        referenceTypeID: 8,
        frameID: 8,
      });
    },
  );
});

test("send resolves with the reply body", async () => {
  await withServer(
    fakeJvm((sock, id) => sock.write(reply(id, 0, Buffer.from("Version!")))),
    async client => {
      const data = await client.send(CommandSet.VirtualMachine, VirtualMachineCmd.Version);
      assert.equal(data.toString(), "Version!");
    },
  );
});

test("a non-zero error code rejects with JdwpError", async () => {
  await withServer(
    fakeJvm((sock, id) => sock.write(reply(id, 0x0d, Buffer.alloc(0)))), // INVALID_OBJECT
    async client => {
      await assert.rejects(
        client.send(CommandSet.ThreadReference, 1),
        (e: unknown) => e instanceof JdwpError && e.code === 0x0d,
      );
    },
  );
});

test("Event.Composite packets reach the event listener", async () => {
  await withServer(
    fakeJvm((sock, id) => {
      sock.write(reply(id, 0, Buffer.alloc(0)));
      sock.write(event(Buffer.from([0xab, 0xcd])));
    }),
    async client => {
      const got = new Promise<Buffer>(resolve => client.onEvent(resolve));
      await client.send(CommandSet.VirtualMachine, VirtualMachineCmd.Resume);
      assert.deepEqual([...(await got)], [0xab, 0xcd]);
    },
  );
});
