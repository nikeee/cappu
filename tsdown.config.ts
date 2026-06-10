import { defineConfig } from "tsdown";

export default defineConfig({
  // serverMain calls startServer(); bundling bare server.ts would only define
  // the server without listening (the extension runs dist/server.mjs).
  entry: { server: "src/serverMain.ts" },
  target: "esnext",
});
