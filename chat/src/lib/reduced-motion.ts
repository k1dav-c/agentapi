// prefersReducedMotion reports whether the viewer asked for less motion:
// what moves on the page jumps instead (false on the server).
export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
