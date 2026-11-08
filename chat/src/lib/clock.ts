// "m:ss", or "h:mm:ss" from an hour on: how long something took.
export function formatDuration(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const pad = (value: number) => String(value).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

// "< 1s", "4.2s", or "3m 5s": how long a tool call took, in milliseconds.
export function formatMilliseconds(ms: number): string {
  if (ms < 1000) return "< 1s";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

// "HH:MM" of an ISO time, in the viewer's time zone; "" for a time that
// doesn't parse.
export function formatTimeOfDay(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleTimeString([], {hour: "2-digit", minute: "2-digit", hour12: false});
}

// Whole seconds from start to end (epoch milliseconds), never negative.
export function secondsBetween(start: number, end: number): number {
  return Math.max(0, Math.floor((end - start) / 1000));
}
