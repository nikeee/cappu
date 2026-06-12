// JDK provisioning API (self-contained, like src/packages/): resolve a
// cappu.json "jdk" entry to a download, cache the archive per user, unpack
// into the project's .cappu/jdks/<spec>.

export {
  type JdkDistribution,
  type JdkSpec,
  jdkDownloadUrl,
  parseJdkSpec,
  projectJdkDir,
  provisionedJavac,
  type ProvisionResult,
  provisionJdk,
} from "./jdks.ts";
