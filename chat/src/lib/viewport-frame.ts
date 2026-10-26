// Where a page's frame goes: the part of the page the browser shows. While
// an on-screen keyboard is up, iOS browsers report it differently (Chrome
// shrinks the visual viewport; others shrink the window and scroll it to
// the field), but the visual viewport is always what is visible, so the
// frame takes its place and size. At rest (no keyboard, nothing scrolled)
// there is none, and the frame keeps to the screen (inset 0), between the
// toolbars; nor while the page is zoomed in.
export function viewportFrame(
  innerHeight: number,
  viewport: {height: number; offsetTop: number; scale?: number} | null,
): {top: number; height: number} | null {
  if (!viewport || (viewport.scale ?? 1) > 1.01) return null;
  const top = Math.max(0, Math.round(viewport.offsetTop));
  const height = Math.round(viewport.height);
  // Small differences are toolbars sliding in and out.
  if (top <= 1 && Math.abs(innerHeight - height) <= 80) return null;
  return {top, height};
}
