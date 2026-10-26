// Keys that can't be typed into a phone's on-screen keyboard (or that the
// browser would intercept), as the bytes a terminal sends for them.

export const ctrlKeys = {
  a: "\x01", // Ctrl+A (start of the line)
  c: "\x03", // Ctrl+C (interrupt)
  d: "\x04", // Ctrl+D (end of input)
  e: "\x05", // Ctrl+E (end of the line)
  l: "\x0C", // Ctrl+L (clear screen)
  r: "\x12", // Ctrl+R (search the history)
  u: "\x15", // Ctrl+U (delete to the start of the line)
  w: "\x17", // Ctrl+W (delete the word before the cursor)
  z: "\x1A", // Ctrl+Z (suspend)
} as const;

export interface TerminalShortcut {
  label: string;
  display: string;
  value: string;
  // Can end or suspend the agent; asks for a second press.
  risky?: boolean;
}

// Most used first (picking an option: arrows and Enter), so they're in
// reach without scrolling the key bar on a phone; then a shell's line
// editing and history, paging, and characters a phone keyboard hides on
// another page.
export const terminalShortcuts: TerminalShortcut[] = [
  {label: "Arrow up", display: "↑", value: "\x1b[A"},
  {label: "Arrow down", display: "↓", value: "\x1b[B"},
  {label: "Enter", display: "Enter", value: "\r"},
  {label: "Escape", display: "Esc", value: "\x1b"},
  {label: "Arrow left", display: "←", value: "\x1b[D"},
  {label: "Arrow right", display: "→", value: "\x1b[C"},
  {label: "Tab", display: "Tab", value: "\t"},
  {label: "Shift+Tab", display: "⇧Tab", value: "\x1b[Z"},
  {label: "Ctrl+C", display: "Ctrl+C", value: ctrlKeys.c},
  {label: "Ctrl+R", display: "Ctrl+R", value: ctrlKeys.r},
  {label: "Ctrl+A", display: "Ctrl+A", value: ctrlKeys.a},
  {label: "Ctrl+E", display: "Ctrl+E", value: ctrlKeys.e},
  {label: "Ctrl+U", display: "Ctrl+U", value: ctrlKeys.u},
  {label: "Ctrl+W", display: "Ctrl+W", value: ctrlKeys.w},
  {label: "Home", display: "Home", value: "\x1b[H"},
  {label: "End", display: "End", value: "\x1b[F"},
  {label: "Page up", display: "PgUp", value: "\x1b[5~"},
  {label: "Page down", display: "PgDn", value: "\x1b[6~"},
  {label: "Slash", display: "/", value: "/"},
  {label: "Hyphen", display: "-", value: "-"},
  {label: "Pipe", display: "|", value: "|"},
  {label: "Tilde", display: "~", value: "~"},
  {label: "Alt+Arrow up", display: "Alt+↑", value: "\x1b[1;3A"},
  {label: "Ctrl+L", display: "Ctrl+L", value: ctrlKeys.l},
  {label: "Ctrl+D", display: "Ctrl+D", value: ctrlKeys.d, risky: true},
  {label: "Ctrl+Z", display: "Ctrl+Z", value: ctrlKeys.z, risky: true},
];

// A modifier held for the next key typed on the keyboard, as a phone's
// keyboard has neither Ctrl nor Alt.
export type StickyModifier = "ctrl" | "alt";

// withModifier is what typing data with modifier held sends: Ctrl turns a
// letter (and @ [ \ ] ^ _ ? and space) into its control character, Alt sends
// Escape first. Ctrl leaves anything else as it is.
export function withModifier(data: string, modifier: StickyModifier): string {
  if (!data) return data;
  if (modifier === "alt") return `\x1b${data}`;
  const [first] = data;
  const rest = data.slice(first.length);
  const upper = first.toUpperCase();
  if (upper >= "@" && upper <= "_") return String.fromCharCode(upper.charCodeAt(0) - 64) + rest;
  if (first === " ") return `\x00${rest}`;
  if (first === "?") return `\x7f${rest}`;
  return data;
}
