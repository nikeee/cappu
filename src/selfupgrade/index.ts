// Self-upgrade API (self-contained, like src/packages/): replace the running
// cappu binary with the latest CD build artifact for this platform.

export {
  type ArtifactRef,
  downloadBinary,
  type FetchBytes,
  type FetchJson,
  latestArtifact,
  platformTarget,
  replaceBinary,
  resolveToken,
  selfUpgrade,
  type UpgradeResult,
  type UpgradeTarget,
} from "./selfupgrade.ts";
