// A size in bytes as the chat shows it: "512 B", "12.1 KB", "3.4 MB".
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

// The exact size, and from a KB on the short one too: "12,345 bytes (12.1
// KB)".
export function formatBytes(bytes: number): string {
  const exact = `${bytes.toLocaleString("en-US")} ${bytes === 1 ? "byte" : "bytes"}`;
  return bytes < 1024 ? exact : `${exact} (${formatSize(bytes)})`;
}
