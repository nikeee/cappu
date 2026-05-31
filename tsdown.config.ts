import { defineConfig } from "tsdown";

export default defineConfig({
  entry: ["src/server.ts"],
  target: "esnext",
});
