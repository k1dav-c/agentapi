// Keys that can't be typed into a phone's on-screen keyboard (or that the
// browser would intercept), as the bytes a terminal sends for them.

export const ctrlKeys = {
  c: "\x03", // Ctrl+C (interrupt)
  d: "\x04", // Ctrl+D (end of input)
  z: "\x1A", // Ctrl+Z (suspend)
  l: "\x0C", // Ctrl+L (clear screen)
} as const;

export interface TerminalShortcut {
  label: string;
  display: string;
  value: string;
  // Can end or suspend the agent; asks for a second press.
  risky?: boolean;
}

export const terminalShortcuts: TerminalShortcut[] = [
  {label: "Escape", display: "Esc", value: "\x1b"},
  {label: "Tab", display: "Tab", value: "\t"},
  {label: "Arrow up", display: "↑", value: "\x1b[A"},
  {label: "Arrow down", display: "↓", value: "\x1b[B"},
  {label: "Alt+Arrow up", display: "Alt+↑", value: "\x1b[1;3A"},
  {label: "Enter", display: "⏎", value: "\r"},
  {label: "Ctrl+C", display: "Ctrl+C", value: ctrlKeys.c},
  {label: "Ctrl+L", display: "Ctrl+L", value: ctrlKeys.l},
  {label: "Ctrl+D", display: "Ctrl+D", value: ctrlKeys.d, risky: true},
  {label: "Ctrl+Z", display: "Ctrl+Z", value: ctrlKeys.z, risky: true},
];
