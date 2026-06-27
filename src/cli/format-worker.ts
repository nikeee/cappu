// worker_threads entry for parallel formatting. Receives a chunk of file paths
// and formats each, posting the outcomes back in input order. The main thread
// (format.ts) reassembles chunks by index and does all writing/output serially,
// so output stays deterministic. The formatter is independent per file and holds
// no shared mutable state, so running it across threads is safe.

import { parentPort, workerData } from "node:worker_threads";

import { type FormatOptions } from "../format/index.ts";
import { formatOne, type Outcome } from "./format-one.ts";

interface WorkerData {
  files: string[];
  cwd: string;
  style: FormatOptions["style"];
}

const { files, cwd, style } = workerData as WorkerData;
const outcomes: Outcome[] = files.map(f => formatOne(f, cwd, style));
parentPort!.postMessage(outcomes);
