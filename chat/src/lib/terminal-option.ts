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

export interface TerminalOption {
  // Keystrokes before terminalOptionKeystrokes: a digit for numbered
  // options, otherwise the raw sequence that selects the option.
  key: string;
  label: string;
}

const numberedOptionRe = /^\s*[❯›>]?\s*(\d+)\.\s+(.+)/;
const cursorOptionRe = /^\s*[❯›]\s+(.+)/;
const hintRe = /to (confirm|cancel)|enter (continue|select)|esc (skip|back|to)/i;

// Extracts the choices of the interactive prompt at the bottom of the
// terminal. Only the last numbered list that starts at 1 is used: the
// prompt text can include the end of the agent's answer, and a numbered
// list there must not be offered as options.
export function parseTerminalOptions(prompt: string): TerminalOption[] {
  const lines = prompt.split("\n");

  let current: TerminalOption[] = [];
  let last: TerminalOption[] = [];
  for (const line of lines) {
    const match = line.match(numberedOptionRe);
    if (!match) continue;
    const number = Number(match[1]);
    if (number === 1) {
      current = [];
    } else if (number !== current.length + 1) {
      current = [];
      continue;
    }
    current.push({key: match[1], label: match[2].trim()});
    last = current;
  }
  if (last.length > 1) return last;

  // Options without numbers (e.g. Claude Code's folder trust dialog): the
  // highlighted one is picked with Enter, the others by moving the cursor.
  const cursor = lines.findIndex((line) => cursorOptionRe.test(line));
  if (cursor !== -1) {
    const isOption = (line: string) => {
      const label = line.trim();
      return /^\s{2,}\S/.test(line) && label.length > 2 && label.length < 80 &&
        !label.startsWith("─") && !hintRe.test(label);
    };
    let start = cursor;
    while (start > 0 && isOption(lines[start - 1])) start--;
    let end = cursor;
    while (end + 1 < lines.length && isOption(lines[end + 1])) end++;
    if (end > start) {
      const options: TerminalOption[] = [];
      for (let i = start; i <= end; i++) {
        const label = lines[i].replace(cursorOptionRe, "$1").trim();
        const moves = i < cursor ? "\x1b[A".repeat(cursor - i) : "\x1b[B".repeat(i - cursor);
        options.push({key: `${moves}\r`, label});
      }
      return options;
    }
  }

  if (/Press Enter/i.test(prompt)) {
    return [{key: "\r", label: "Press Enter to continue"}];
  }
  return [];
}
