// Detect when cappu is invoked by an AI agent / coding assistant rather than a
// human at a terminal. Mirrors the CI=true convention. The cross-tool proposal
// is `AGENT` (https://github.com/agentsmd/agents.md/issues/136); until that is
// universal we also honour the per-tool vars tools set today. Any of these set
// and non-empty means "an agent is driving us".
//
// When an agent is detected, coloured/animated output is implied off (the agent
// reads raw text, not a TTY), the same way NO_COLOR opts out - see colorEnabled.

// Per-tool environment variables, plus the proposed cross-tool `AGENT`. Presence
// with a non-empty value is the signal; we do not match specific values so new
// agents that follow the convention work without a code change.
const AGENT_ENV_VARS = [
  "AGENT", // cross-tool proposal (Goose, Amp, ...)
  "AI_AGENT", // Vercel detect-agent
  "CLAUDECODE", // Claude Code
  "CURSOR_AGENT", // Cursor
  "GEMINI_CLI", // Gemini CLI
  "AUGMENT_AGENT", // Augment
  "CLINE_ACTIVE", // Cline
  "OPENCODE_CLIENT", // OpenCode
  "TRAE_AI_SHELL_ID", // TRAE AI
  "CODEX_SANDBOX", // Codex
] as const;

/** Whether an AI agent (not a human terminal) is driving cappu. */
export function agentEnabled(env: NodeJS.ProcessEnv = process.env): boolean {
  return AGENT_ENV_VARS.some(name => {
    const value = env[name];
    return value !== undefined && value !== "";
  });
}
