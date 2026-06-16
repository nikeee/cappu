// Shared CLI styling: a colour function bound to a stream (a no-op when colour
// is disabled, see color.ts) and a short duration formatter for the timing
// footer main.ts prints after the package/build commands.

import { styleText } from "node:util";

import { SingleBar } from "cli-progress";

import { colorEnabled } from "./color.ts";

export type StyleFormat = Parameters<typeof styleText>[0];

/**
 * The shared download progress bar (jars, JDK archives, the self-upgrade
 * binary): a coloured bar with a {value}/{total} count and a {package} label.
 * `unit` (e.g. "MiB") is shown after the count when the value/total are not a
 * plain item count. Undefined when `stream` is not a colour-capable TTY, so
 * piped output stays plain. styleText is bound to `stream` so colours drop out
 * for it too.
 */
export function downloadBar(
  stream: NodeJS.WriteStream,
  options: { unit?: string } = {},
): SingleBar | undefined {
  if (!colorEnabled(stream.isTTY)) return undefined;
  const style = (format: StyleFormat, text: string): string => styleText(format, text, { stream });
  const count = options.unit ? `{value}/{total} ${options.unit}` : "{value}/{total}";
  return new SingleBar({
    format: `${style("cyan", "{bar}")} ${style("bold", count)} ${style("dim", "{package}")}`,
    barCompleteChar: "█",
    barIncompleteChar: "░",
    hideCursor: true,
    clearOnComplete: true,
    stream,
  });
}

/** A colour function for `stream`; returns the text unchanged without colour. */
export function painter(stream: NodeJS.WriteStream): (format: StyleFormat, text: string) => string {
  const on = colorEnabled(stream.isTTY);
  return (format, text) => (on ? styleText(format, text, { stream }) : text);
}

/** A short human duration: "850ms" under a second, otherwise "1.2s". */
export function formatDuration(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`;
}
