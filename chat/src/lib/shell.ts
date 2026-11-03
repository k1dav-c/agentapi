// Helpers for the shell terminal (GET /terminal), streamed to xterm.

// The bytes of a base64 string, as the terminal stream sends its output.
export function decodeBase64(data: string): Uint8Array {
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

// The terminal's size in cells for a panel of width × height pixels, or
// null until the cells can be measured. It never gets too small to use.
export function terminalSize(width: number, height: number, cellWidth: number, cellHeight: number): {cols: number; rows: number} | null {
  if (cellWidth <= 0 || cellHeight <= 0 || width <= 0 || height <= 0) return null;
  return {
    cols: Math.max(20, Math.floor(width / cellWidth)),
    rows: Math.max(5, Math.floor(height / cellHeight)),
  };
}

// Sends keystrokes one request at a time so they reach the terminal in the
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
