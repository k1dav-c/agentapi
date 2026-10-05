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

// Codex's mark: the OpenAI logo, as on Codex's own pages. It is one color,
// drawn in the text color so it follows the theme.
export const CODEX_MARK = {
  viewBox: "0 0 24 24",
  d: "M22.418 9.822a5.903 5.903 0 0 0-.52-4.91 6.1 6.1 0 0 0-2.822-2.513 6.204 6.204 0 0 0-3.78-.389A6.055 6.055 0 0 0 13.232.518 6.129 6.129 0 0 0 10.726 0a6.185 6.185 0 0 0-3.615 1.153A6.052 6.052 0 0 0 4.88 4.187a6.102 6.102 0 0 0-2.344 1.018A6.008 6.008 0 0 0 .828 7.087a5.981 5.981 0 0 0 .754 7.09 5.904 5.904 0 0 0 .52 4.911 6.101 6.101 0 0 0 2.821 2.513 6.205 6.205 0 0 0 3.78.389 6.057 6.057 0 0 0 2.065 1.492 6.13 6.13 0 0 0 2.505.518 6.185 6.185 0 0 0 3.617-1.154 6.052 6.052 0 0 0 2.232-3.035 6.101 6.101 0 0 0 2.343-1.018 6.009 6.009 0 0 0 1.709-1.883 5.981 5.981 0 0 0-.756-7.088Zm-9.143 12.609a4.583 4.583 0 0 1-2.918-1.04c.037-.02.102-.056.144-.081l4.844-2.76a.783.783 0 0 0 .397-.68v-6.738L17.79 12.3a.072.072 0 0 1 .04.055v5.58a4.473 4.473 0 0 1-1.335 3.176 4.596 4.596 0 0 1-3.219 1.321Zm-9.793-4.127a4.432 4.432 0 0 1-.544-3.014c.036.021.099.06.144.085l4.843 2.76a.796.796 0 0 0 .795 0l5.913-3.369V17.1a.071.071 0 0 1-.029.062L9.708 19.95a4.617 4.617 0 0 1-3.458.447 4.556 4.556 0 0 1-2.768-2.093ZM2.208 7.872A4.527 4.527 0 0 1 4.58 5.9l-.002.164v5.52a.768.768 0 0 0 .397.68l5.913 3.369-2.047 1.166a.075.075 0 0 1-.069.006l-4.896-2.792a4.51 4.51 0 0 1-2.12-2.73 4.45 4.45 0 0 1 .452-3.411Zm16.818 3.861-5.913-3.368 2.047-1.166a.074.074 0 0 1 .07-.006l4.896 2.789a4.526 4.526 0 0 1 1.762 1.815 4.448 4.448 0 0 1-.418 4.808 4.556 4.556 0 0 1-2.049 1.494v-5.686a.767.767 0 0 0-.395-.68Zm2.038-3.025a6.874 6.874 0 0 0-.144-.085l-4.843-2.76a.797.797 0 0 0-.796 0L9.368 9.23V6.9a.072.072 0 0 1 .03-.062l4.895-2.787a4.608 4.608 0 0 1 4.885.207 4.51 4.51 0 0 1 1.599 1.955c.333.788.433 1.654.287 2.496ZM8.255 12.865 6.208 11.7a.071.071 0 0 1-.04-.056v-5.58c0-.854.248-1.69.713-2.412a4.54 4.54 0 0 1 1.913-1.658 4.614 4.614 0 0 1 4.85.616c-.037.02-.102.055-.144.08L8.657 5.452a.782.782 0 0 0-.398.68l-.004 6.734ZM9.367 10.5 12.001 9l2.633 1.5v3L12.001 15l-2.634-1.5v-3Z",
};

// The square's color, for agents drawn as a tinted square. CSS variables
// for the page; the tab icon can't read them and uses the literal.
const SQUARE_TINTS: Record<string, {css: string; literal: string}> = {
  claude: {css: "var(--agent-claude)", literal: "#d97757"},
};
const DEFAULT_TINT = {css: "var(--foreground)", literal: "#e8eaf0"};

export function hasLogo(agentType: string): boolean {
  return agentType === "pi" || agentType === "codex";
}

export function squareTint(agentType: string): {css: string; literal: string} {
  return SQUARE_TINTS[agentType] ?? DEFAULT_TINT;
}

// SVG markup for the agent mark inside the square (x, y, size), for the
// tab icon.
export function agentMarkSvg(agentType: string, x: number, y: number, size: number): string {
  if (agentType === "codex") {
    // Light on the icon's dark tile.
    return `<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="${CODEX_MARK.viewBox}"><path fill="#e8eaf0" d="${CODEX_MARK.d}"/></svg>`;
  }
  if (hasLogo(agentType)) {
    const paths = PI_MARK.paths.map((path) => `<path fill="${path.fill}" d="${path.d}"/>`).join("");
    return `<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="${PI_MARK.viewBox}">${paths}</svg>`;
  }
  return `<rect x="${x}" y="${y}" width="${size}" height="${size}" rx="${size / 4}" fill="${squareTint(agentType).literal}"/>`;
}
