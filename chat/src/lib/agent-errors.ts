// What the chat knows an agent_error event by, to show its toast once. The
// server sends its stored errors again on every connection, each with the
// time it happened. A warning shows once per page whenever it comes back:
// an agent repeats some on every start (Codex's note about a setting in its
// config file it ignores), and a restart would show them again.
export function agentErrorKey(error: {message: string; level: string; time: string}): string {
  return error.level === "warning" ? `warning\u0000${error.message}` : `${error.time}\u0000${error.message}`;
}
