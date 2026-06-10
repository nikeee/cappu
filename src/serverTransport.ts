// The stream pair the language server speaks JSON-RPC over. server.ts creates
// its connection at module load, so the CLI configures the transport BEFORE
// lazily importing it: stdio by default, or an accepted TCP socket (--port).

export interface Transport {
  reader: NodeJS.ReadableStream;
  writer: NodeJS.WritableStream;
}

let transport: Transport | undefined;

export function setTransport(reader: NodeJS.ReadableStream, writer: NodeJS.WritableStream): void {
  transport = { reader, writer };
}

export function getTransport(): Transport {
  return transport ?? { reader: process.stdin, writer: process.stdout };
}
