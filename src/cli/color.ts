// Shared colour/animation gate for the CLI: a terminal that is a TTY and has
// not opted out via NO_COLOR (https://no-color.org - set and non-empty). Used
// by the install progress bar/indicator and the audit report. An AI agent
// driving cappu also implies NO_COLOR - see agentEnabled.

import { agentEnabled } from "./agent.ts";

/** Whether coloured / animated output may render on `stream`. */
export function colorEnabled(
  isTTY: boolean | undefined,
  env: NodeJS.ProcessEnv = process.env,
): boolean {
  // TERM=dumb: styleText would refuse anyway; gate here so both builds (and
  // the progress bar/indicator) agree.
  return isTTY === true && !env.NO_COLOR && env.TERM !== "dumb" && !agentEnabled(env);
}
