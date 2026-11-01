// The width of the hub's list on a desktop, beside the overview or an open
// chat; its edge drags it. At 280px a row still shows its name, state and
// age; never more than 640px or half the window, so the other side keeps at
// least as much.
export const DEFAULT_LIST_WIDTH = 360;
export const MIN_LIST_WIDTH = 280;
const MAX_LIST_WIDTH = 640;

// The arrows on the focused edge move it by STEP, with Shift by BIG_STEP.
const STEP = 16;
const BIG_STEP = 64;

// The widest the list may be in a window this wide.
export function maxListWidth(windowWidth: number): number {
  return Math.max(MIN_LIST_WIDTH, Math.min(MAX_LIST_WIDTH, Math.floor(windowWidth / 2)));
}

// clampListWidth fits a width to the window, in whole pixels.
export function clampListWidth(width: number, windowWidth: number): number {
  return Math.min(maxListWidth(windowWidth), Math.max(MIN_LIST_WIDTH, Math.round(width)));
}

// parseListWidth reads the width a browser stored: anything but a positive
// number is the default. It is clamped to the window where it is used.
export function parseListWidth(stored: string | null): number {
  const width = Number(stored);
  return Number.isFinite(width) && width > 0 ? width : DEFAULT_LIST_WIDTH;
}

// keyListWidth is the width a key on the focused edge moves the list to:
// the arrows narrow or widen it, Home and End make it the narrowest and
// widest, Enter brings back the default. Other keys, and keys with Alt,
// Ctrl or Meta (the browser's), are left alone: undefined.
export function keyListWidth(
  event: {key: string; shiftKey?: boolean; altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean},
  width: number,
  windowWidth: number,
): number | undefined {
  if (event.altKey || event.ctrlKey || event.metaKey) return undefined;
  const step = event.shiftKey ? BIG_STEP : STEP;
  switch (event.key) {
    case "ArrowLeft":
      return clampListWidth(width - step, windowWidth);
    case "ArrowRight":
      return clampListWidth(width + step, windowWidth);
    case "Home":
      return MIN_LIST_WIDTH;
    case "End":
      return maxListWidth(windowWidth);
    case "Enter":
      return clampListWidth(DEFAULT_LIST_WIDTH, windowWidth);
    default:
      return undefined;
  }
}
