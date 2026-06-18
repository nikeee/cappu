import { defineConfig } from "tsdown";

const NODE_VERSION = "26.3.0";

// Bundle every dependency (anything that is not a node: builtin) so the
// outputs are self-contained: the cli bundle ships without node_modules, and a
// SEA has nothing to resolve against at all.
const noExternal = /^(?!node:)/;

export default defineConfig([
  { entry: { server: "src/services/serverMain.ts" }, target: "esnext" },
  // cli.mjs used for docker
  { entry: { cli: "src/cli/main.ts" }, target: "esnext", format: "esm", noExternal },
  ...(process.env.CAPPU_SKIP_EXE
    ? []
    : [
        {
          entry: ["src/cli/main.ts"],
          noExternal,
          exe: {
            fileName: "cappu",
            outDir: "dist",
            targets: [
              { platform: "linux", arch: "x64", nodeVersion: NODE_VERSION } as const,
              { platform: "linux", arch: "arm64", nodeVersion: NODE_VERSION } as const,
              { platform: "darwin", arch: "arm64", nodeVersion: NODE_VERSION } as const,
              { platform: "win", arch: "x64", nodeVersion: NODE_VERSION } as const,
            ],
          },
        },
      ]),
]);
