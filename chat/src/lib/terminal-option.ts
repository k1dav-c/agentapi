// Agents whose prompts pick a numbered option as soon as its digit is typed.
const DIGIT_SELECTS = new Set(["claude", "codex"]);

// Keystrokes that pick an option in an agent's interactive terminal prompt.
//
// Claude Code and Codex select a numbered option as soon as its digit is
// typed, so no Enter follows it: a trailing Enter would submit an empty
// feedback field, answer the next question with its default, or land on the
// next prompt and approve it. Other agents need Enter to confirm a numbered
// choice. Non-numeric keys (Enter, arrow sequences) are sent as-is.
export function terminalOptionKeystrokes(agentType: string, key: string): string {
  if (!/^\d$/.test(key)) return key;
  return DIGIT_SELECTS.has(agentType) ? key : `${key}\r`;
}
