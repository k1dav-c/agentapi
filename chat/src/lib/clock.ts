// "m:ss", or "h:mm:ss" from an hour on: how long something took.
export function formatDuration(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const pad = (value: number) => String(value).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
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
