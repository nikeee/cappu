// Shared CLI styling: a colour function bound to a stream (a no-op when colour
// is disabled, see color.ts) and a short duration formatter for the timing
// footer main.ts prints after the package/build commands.

import { styleText } from "node:util";

import { colorEnabled } from "./color.ts";

export type StyleFormat = Parameters<typeof styleText>[0];

/** A colour function for `stream`; returns the text unchanged without colour. */
export function painter(stream: NodeJS.WriteStream): (format: StyleFormat, text: string) => string {
  const on = colorEnabled(stream.isTTY);
  return (format, text) => (on ? styleText(format, text, { stream }) : text);
}

/** A short human duration: "850ms" under a second, otherwise "1.2s". */
export function formatDuration(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`;
}
