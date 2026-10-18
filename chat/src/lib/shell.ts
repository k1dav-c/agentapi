// Helpers for the native transport's terminal, a shell streamed to xterm.

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
