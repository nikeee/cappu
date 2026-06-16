import { defineConfig } from "tsdown";

// The Node version baked into the cross-compiled binaries (@tsdown/exe needs
// >= 25.7.0; cappu targets Node 26).
const NODE_VERSION = "26.3.0";

// Bundle every dependency (anything that is not a node: builtin) so the
// outputs are self-contained: the cli bundle ships without node_modules, and a
// SEA has nothing to resolve against at all.
const noExternal = /^(?!node:)/;

export default defineConfig([
  // The LSP server bundle (dist/server.mjs). serverMain calls startServer();
  // bundling bare server.ts would only define the server without listening.
  { entry: { server: "src/services/serverMain.ts" }, target: "esnext" },
  // A plain CLI bundle (dist/cli.mjs) the Docker image runs under node - no
  // embedded runtime needed there, the image already has node.
  { entry: { cli: "src/cli/main.ts" }, target: "esnext", format: "esm", noExternal },
  // The distributed single-file binaries: Node Single Executable Applications
  // cross-compiled with @tsdown/exe (no Bun runtime) into dist/cappu-<os>-<arch>.
  // macOS x64 and Alpine/musl are unsupported by Node SEA, so they are not
  // built. Skipped (empty spread) for the Docker image, which only needs the
  // cli bundle above.
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
