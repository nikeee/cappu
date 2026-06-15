// Vulnerability-audit API (self-contained, like src/packages/): scan resolved
// Maven coordinates against a CVE source (OSV by default).

export { auditPackages } from "./audit.ts";
export { cveAliases, type FetchJson, fixedVersionsOf, OsvSource, osvSeverity } from "./osv.ts";
export {
  type Advisory,
  type AuditReport,
  type AuditSource,
  type PackageAdvisories,
  type Severity,
  SEVERITY_ORDER,
} from "./types.ts";
