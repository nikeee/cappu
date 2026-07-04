// Upload built artifacts to a Maven registry (maven2 layout, HTTP PUT). Print-
// free and the PUT is injectable, so the CLI renders progress and tests run
// without a network. Phase 1 uploads the jar and the pom, each with the .md5 /
// .sha1 sidecars Maven repositories expect.

import { hash } from "node:crypto";

import { type Brand } from "../brand.ts";
import { DEFAULT_PUBLISH_REGISTRY } from "../config.ts";
import { type Coordinates } from "../packages/index.ts";

/** A registry bearer token (a credential), distinct from arbitrary strings. */
export type BearerToken = Brand<string, "BearerToken">;

/**
 * The registry `cappu publish` uploads to, resolved npm-style (highest wins):
 * the --repo flag, then $CAPPU_PUBLISH_REGISTRY, then cappu.json's
 * publishRepository, then the built-in default (Maven Central).
 */
export function resolvePublishRegistry(
  flag: string | undefined,
  configRepo: string | undefined,
  env: NodeJS.ProcessEnv = process.env,
): string {
  // || not ??: an empty string (e.g. CAPPU_PUBLISH_REGISTRY=) means "unset",
  // like the Go build - never a registry URL of "".
  return flag || env.CAPPU_PUBLISH_REGISTRY || configRepo || DEFAULT_PUBLISH_REGISTRY;
}

export type PublishAuth =
  | { type: "basic"; username: string; password: string }
  | { type: "bearer"; token: BearerToken };

/** The maven2 path for a file: group dots become directories. */
export function maven2Path(coordinates: Coordinates, filename: string): string {
  return [
    ...coordinates.groupId.split("."),
    coordinates.artifactId,
    coordinates.version,
    filename,
  ].join("/");
}

/**
 * Publishing credentials from the environment: Basic when a username+password
 * pair is set, else a Bearer token, else undefined (the CLI then errors).
 */
export function resolvePublishAuth(env: NodeJS.ProcessEnv = process.env): PublishAuth | undefined {
  const username = env.CAPPU_PUBLISH_USERNAME;
  const password = env.CAPPU_PUBLISH_PASSWORD;
  if (username && password) return { type: "basic", username, password };
  if (env.CAPPU_PUBLISH_TOKEN)
    return { type: "bearer", token: env.CAPPU_PUBLISH_TOKEN as BearerToken };
  return undefined;
}

function authorizationHeader(auth: PublishAuth): string {
  return auth.type === "basic"
    ? `Basic ${Buffer.from(`${auth.username}:${auth.password}`).toString("base64")}`
    : `Bearer ${auth.token}`;
}

export interface PublishFile {
  filename: string;
  bytes: Uint8Array;
}

export type PutFn = (
  url: string,
  bytes: Uint8Array,
  authorization: string | undefined,
) => Promise<void>;

const defaultPut: PutFn = async (url, bytes, authorization) => {
  const response = await fetch(url, {
    method: "PUT",
    headers: {
      "content-type": "application/octet-stream",
      ...(authorization ? { authorization } : {}),
    },
    body: bytes,
  });
  if (!response.ok) throw new Error(`upload failed: HTTP ${response.status} for ${url}`);
};

// The .md5 / .sha1 sidecar files a Maven repository expects beside each artifact.
function checksumSidecars(file: PublishFile): PublishFile[] {
  return [
    { filename: `${file.filename}.md5`, bytes: hexBytes(hash("md5", file.bytes, "hex")) },
    { filename: `${file.filename}.sha1`, bytes: hexBytes(hash("sha1", file.bytes, "hex")) },
  ];
}

function hexBytes(hex: string): Uint8Array {
  return new TextEncoder().encode(hex);
}

/**
 * PUT every file (and its checksum sidecars) to `repo` under the maven2 layout
 * for `coordinates`. Returns the uploaded urls in order; throws on the first
 * non-2xx response.
 */
export async function publishArtifacts(options: {
  repo: string;
  coordinates: Coordinates;
  files: readonly PublishFile[];
  auth?: PublishAuth;
  put?: PutFn;
  onUpload?: (url: string) => void;
}): Promise<string[]> {
  const put = options.put ?? defaultPut;
  const authorization = options.auth ? authorizationHeader(options.auth) : undefined;
  const base = options.repo.endsWith("/") ? options.repo : `${options.repo}/`;
  const uploaded: string[] = [];
  for (const file of options.files.flatMap(f => [f, ...checksumSidecars(f)])) {
    const url = new URL(maven2Path(options.coordinates, file.filename), base).href;
    options.onUpload?.(url);
    await put(url, file.bytes, authorization);
    uploaded.push(url);
  }
  return uploaded;
}
