// Helpers for TTY mode, which mirrors the agent's emulated terminal screen
// in xterm.js and sends keystrokes straight to the agent.

// Terminal output that replaces everything xterm shows with `screen`, the
// plain-text snapshot AgentAPI keeps of the agent's terminal: home, clear
// the screen and the scrollback, then write the lines.
//
// AgentAPI's terminal is very tall (1000 rows by default), so an agent's
// transcript and its input box drawn at the bottom are separated by
// hundreds of blank rows. Runs of blank lines are collapsed to one so the
// conversation sits right above the input box, as in a normal terminal.
export function screenToTerminalOutput(screen: string): string {
  const lines: string[] = [];
  for (const line of screen.split(/\r?\n/)) {
    const trimmed = line.trimEnd();
    if (trimmed === "" && lines.length > 0 && lines[lines.length - 1] === "") continue;
    lines.push(trimmed);
  }
  while (lines.length > 0 && lines[0] === "") lines.shift();
  return "\x1b[H\x1b[2J\x1b[3J" + lines.join("\r\n");
}

// Sends keystrokes one request at a time so they reach the agent in the
// order they were typed. Keys typed while a request is in flight are
// batched into the next one.
export class OrderedInput {
  private pending = "";
  private sending = false;

  constructor(
    private readonly send: (data: string) => Promise<void>,
    private readonly onError: (error: unknown) => void = () => {},
  ) {}

  push(data: string): void {
    this.pending += data;
    void this.flush();
  }

  private async flush(): Promise<void> {
    if (this.sending || this.pending === "") return;
    const chunk = this.pending;
    this.pending = "";
    this.sending = true;
    try {
      await this.send(chunk);
    } catch (error) {
      this.onError(error);
    } finally {
      this.sending = false;
    }
    await this.flush();
  }
}
