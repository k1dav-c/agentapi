// The small mark that stands for the agent in the header and the tab icon.
// Agents with a recognisable logo get it; the others a tinted square.

export interface MarkPath {
  fill: string;
  d: string;
}

// The Pi logo from pi.dev: a block-letter π in coral, blue and amber.
export const PI_MARK = {
  viewBox: "0 0 469.43 469.43",
  paths: [
    {fill: "#F09082", d: "M0 0H352.07V234.71H234.71V117.36H0Z"},
    {fill: "#4D9ABF", d: "M0 117.36H117.36V234.71H234.71V352.07H117.36V469.43H0Z"},
    {fill: "#F1BE58", d: "M352.07 234.71H469.43V469.43H352.07Z"},
  ] satisfies MarkPath[],
};

// The square's color, for agents drawn as a tinted square. CSS variables
// for the page; the tab icon can't read them and uses the literal.
const SQUARE_TINTS: Record<string, {css: string; literal: string}> = {
  claude: {css: "var(--agent-claude)", literal: "#d97757"},
};
const DEFAULT_TINT = {css: "var(--foreground)", literal: "#e8eaf0"};

export function hasLogo(agentType: string): boolean {
  return agentType === "pi";
}

export function squareTint(agentType: string): {css: string; literal: string} {
  return SQUARE_TINTS[agentType] ?? DEFAULT_TINT;
}

// SVG markup for the agent mark inside the square (x, y, size), for the
// tab icon.
export function agentMarkSvg(agentType: string, x: number, y: number, size: number): string {
  if (hasLogo(agentType)) {
    const paths = PI_MARK.paths.map((path) => `<path fill="${path.fill}" d="${path.d}"/>`).join("");
    return `<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="${PI_MARK.viewBox}">${paths}</svg>`;
  }
  return `<rect x="${x}" y="${y}" width="${size}" height="${size}" rx="${size / 4}" fill="${squareTint(agentType).literal}"/>`;
}
