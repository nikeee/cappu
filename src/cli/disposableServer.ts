import { createServer, type Server } from "node:net";

/** A listening TCP server that closes itself when its `using` scope exits. */
export function listenDisposable(port: number): Server & Disposable {
  const server = createServer().listen(port);
  return Object.assign(server, { [Symbol.dispose]: () => void server.close() });
}
