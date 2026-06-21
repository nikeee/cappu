// Spawn the debuggee JVM with the JDWP agent listening, and report the port it
// chose. `server=y,suspend=y,address=127.0.0.1:0` makes the JVM listen on an
// ephemeral loopback port and freeze before main runs; the agent prints the
// chosen port on stdout ("Listening for transport dt_socket at address: NNNNN"),
// which we parse so the session can attach as a JDWP client. Because the VM is
// suspended, no program output follows until the caller resumes it, so consuming
// just this line off stdout loses nothing. The caller owns the returned process
// (stdout/stderr forwarding, exit handling).
//
// Port reference for togo/internal/dapserver/launch.go.

import { type ChildProcessByStdio, spawn } from "node:child_process";
import type { Readable } from "node:stream";

// stdin is ignored; stdout/stderr are piped so the session can forward them.
export type DebuggeeProcess = ChildProcessByStdio<null, Readable, Readable>;

export interface Launched {
  process: DebuggeeProcess;
  port: number;
}

const LISTENING = /Listening for transport dt_socket at address:\s*(\d+)/;

export function jdwpAgentArg(): string {
  return "-agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=127.0.0.1:0";
}

export function launchUnderJdwp(
  java: string,
  classPath: string,
  mainClass: string,
  programArgs: string[] = [],
): Promise<Launched> {
  const child = spawn(java, [jdwpAgentArg(), "-cp", classPath, mainClass, ...programArgs], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  return new Promise((resolve, reject) => {
    let settled = false;
    let stdoutBuf = "";
    const onOut = (chunk: Buffer) => {
      stdoutBuf += chunk.toString("utf8");
      const m = LISTENING.exec(stdoutBuf);
      if (m && !settled) {
        settled = true;
        child.stdout.off("data", onOut);
        resolve({ process: child, port: Number(m[1]) });
      }
    };
    child.stdout.on("data", onOut);
    child.once("error", e => {
      if (!settled) {
        settled = true;
        reject(e);
      }
    });
    child.once("exit", code => {
      if (!settled) {
        settled = true;
        reject(
          new Error(`debuggee exited before the JDWP agent listened (code ${code})\n${stdoutBuf}`),
        );
      }
    });
  });
}
