import { hash } from "node:crypto";
import { test } from "node:test";

import { expect } from "expect";

import {
  maven2Path,
  type PutFn,
  publishArtifacts,
  resolvePublishAuth,
  resolvePublishRegistry,
} from "./publish.ts";

const COORDS = { groupId: "com.example", artifactId: "my-lib", version: "1.2.0" };

test("maven2Path lays group dots out as directories", () => {
  expect(maven2Path(COORDS, "my-lib-1.2.0.jar")).toBe("com/example/my-lib/1.2.0/my-lib-1.2.0.jar");
});

test("resolvePublishAuth prefers basic, then bearer, else undefined", () => {
  expect(resolvePublishAuth({ CAPPU_PUBLISH_USERNAME: "u", CAPPU_PUBLISH_PASSWORD: "p" })).toEqual({
    type: "basic",
    username: "u",
    password: "p",
  });
  expect(resolvePublishAuth({ CAPPU_PUBLISH_TOKEN: "t" })).toEqual({ type: "bearer", token: "t" });
  expect(resolvePublishAuth({})).toBeUndefined();
});

test("resolvePublishRegistry follows the npm-style precedence, else Maven Central", () => {
  const flag = "https://flag.example/releases";
  const cfg = "https://config.example/releases";
  const envWith = { CAPPU_PUBLISH_REGISTRY: "https://env.example/releases" };
  expect(resolvePublishRegistry(flag, cfg, envWith)).toBe(flag); // --repo wins
  expect(resolvePublishRegistry(undefined, cfg, envWith)).toBe(envWith.CAPPU_PUBLISH_REGISTRY);
  expect(resolvePublishRegistry(undefined, cfg, {})).toBe(cfg);
  expect(resolvePublishRegistry(undefined, undefined, {})).toBe(
    "https://repo.maven.apache.org/maven2",
  );
});

test("publishArtifacts PUTs each file with md5/sha1 sidecars under the maven2 layout", async () => {
  const jar = new TextEncoder().encode("jar-bytes");
  const calls: { url: string; bytes: Uint8Array; authorization?: string }[] = [];
  const put: PutFn = (url, bytes, authorization) => {
    calls.push({ url, bytes, authorization: authorization ?? undefined });
    return Promise.resolve();
  };

  const uploaded = await publishArtifacts({
    repo: "https://maven.example.com/releases",
    coordinates: COORDS,
    files: [{ filename: "my-lib-1.2.0.jar", bytes: jar }],
    auth: { type: "basic", username: "u", password: "p" },
    put,
  });

  const dir = "https://maven.example.com/releases/com/example/my-lib/1.2.0";
  expect(uploaded).toEqual([
    `${dir}/my-lib-1.2.0.jar`,
    `${dir}/my-lib-1.2.0.jar.md5`,
    `${dir}/my-lib-1.2.0.jar.sha1`,
  ]);
  // every request carried the Basic auth header
  const basic = `Basic ${Buffer.from("u:p").toString("base64")}`;
  expect(calls.every(c => c.authorization === basic)).toBe(true);
  // the sidecars carry the hex digests of the jar
  expect(new TextDecoder().decode(calls[1]!.bytes)).toBe(hash("md5", jar, "hex"));
  expect(new TextDecoder().decode(calls[2]!.bytes)).toBe(hash("sha1", jar, "hex"));
});

test("publishArtifacts rejects when an upload fails", async () => {
  const put: PutFn = url => Promise.reject(new Error(`HTTP 401 for ${url}`));
  await expect(
    publishArtifacts({
      repo: "https://maven.example.com/releases",
      coordinates: COORDS,
      files: [{ filename: "my-lib-1.2.0.jar", bytes: new Uint8Array([1]) }],
      put,
    }),
  ).rejects.toThrow("HTTP 401");
});
