// GitHub Actions / Forgejo / Gitea workflow-command annotations. When cappu runs
// inside a runner that parses GitHub-style workflow commands, errors and warnings
// are echoed as `::error file=...,line=...::message` so they surface as inline
// annotations, in addition to the normal stderr output.
// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands

export type AnnotationSeverity = "error" | "warning";

export interface AnnotationLocation {
  file?: string;
  line?: number;
  column?: number;
}

/**
 * Whether the current runner understands GitHub-style workflow commands.
 * Forgejo/Gitea Actions also set GITHUB_ACTIONS=true for compatibility, but we
 * check their own vars too so the intent is explicit. Bare CI=true is not a
 * trigger: there is no generic-CI annotation format, and emitting GitHub syntax
 * in a non-GitHub runner would just be noise.
 */
export function annotationsEnabled(env: NodeJS.ProcessEnv = process.env): boolean {
  return (
    env.GITHUB_ACTIONS === "true" || env.FORGEJO_ACTIONS === "true" || env.GITEA_ACTIONS === "true"
  );
}

// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands#example-setting-an-error-message
function escapeData(s: string): string {
  return s.replaceAll("%", "%25").replaceAll("\r", "%0D").replaceAll("\n", "%0A");
}

function escapeProp(s: string): string {
  return escapeData(s).replaceAll(":", "%3A").replaceAll(",", "%2C");
}

/** Format one workflow-command annotation line (no trailing newline). */
export function formatAnnotation(
  severity: AnnotationSeverity,
  message: string,
  loc?: AnnotationLocation,
): string {
  const props: string[] = [];
  if (loc?.file !== undefined) props.push(`file=${escapeProp(loc.file)}`);
  if (loc?.line !== undefined) props.push(`line=${escapeProp(String(loc.line))}`);
  if (loc?.column !== undefined) props.push(`col=${escapeProp(String(loc.column))}`);
  const head = props.length > 0 ? `::${severity} ${props.join(",")}` : `::${severity}`;
  return `${head}::${escapeData(message)}`;
}

/** Echo an annotation to stderr when running under a supporting CI runner. */
export function emitAnnotation(
  severity: AnnotationSeverity,
  message: string,
  loc?: AnnotationLocation,
): void {
  if (annotationsEnabled()) {
    process.stderr.write(`${formatAnnotation(severity, message, loc)}\n`);
  }
}
