// Publishing API (self-contained, like src/packages/ and src/audit/): generate
// a POM and upload built artifacts to a Maven registry.

export { generatePom, missingCoordinates } from "./pom.ts";
export {
  maven2Path,
  type PublishAuth,
  type PublishFile,
  publishArtifacts,
  type PutFn,
  resolvePublishAuth,
  resolvePublishRegistry,
} from "./publish.ts";
