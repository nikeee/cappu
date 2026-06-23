// Self-upgrade API (self-contained, like src/packages/): replace the running
// cappu binary with the latest published release asset for this platform.

export {
  downloadBinary,
  type FetchBytes,
  type FetchJson,
  latestRelease,
  platformTarget,
  type ReleaseRef,
  replaceBinary,
  sameVersion,
  selfUpgrade,
  type UpgradeResult,
} from "./selfupgrade.ts";
