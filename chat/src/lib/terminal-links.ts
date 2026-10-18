// The web links in a terminal's lines, also ones that run over several
// lines: the terminal wrapped a long line (a soft wrap), or the program
// broke it at the terminal's width itself.

export interface TerminalLine {
  text: string;
  // Whether the terminal wrapped the previous line onto this one.
  wrapped: boolean;
  // The cell column of each character of text; by default its index (a
  // wide character takes two cells).
  columns?: number[];
}

export interface TerminalPosition {
  // The index of the line in the lines given, and the cell column.
  line: number;
  column: number;
}

export interface TerminalLink {
  url: string;
  start: TerminalPosition;
  // The last cell of the link.
  end: TerminalPosition;
}

const urlStart = /https?:\/\//g;
const urlChars = /^[A-Za-z0-9\-._~:/?#[\]@!$&'()*+,;=%]+/;

// Punctuation after a link, and Markdown or quotes around it, aren't part
// of it.
const trailing = /[),.;:!?*_~`'"\]>]+$/u;

// A line is joined to the one before when the terminal wrapped it, or when
// the one before filled the terminal's width with a link that the program
// broke there and this one carries on with link characters.
function continues(previous: TerminalLine, line: TerminalLine, cols: number): boolean {
  if (line.wrapped) return true;
  const before = previous.text.trimEnd();
  return before.length >= cols - 1 && /\S$/.test(before) && urlChars.test(line.text) && !/\s/.test(line.text.trim());
}

// The groups of lines that read as one, by the index of their first line.
export function logicalLines(lines: TerminalLine[], cols: number): number[][] {
  const groups: number[][] = [];
  lines.forEach((line, index) => {
    const group = groups[groups.length - 1];
    if (group && continues(lines[index - 1], line, cols)) group.push(index);
    else groups.push([index]);
  });
  return groups;
}

// findTerminalLinks returns the http(s) links in lines, in order, with the
// cells they take. cols is the terminal's width.
export function findTerminalLinks(lines: TerminalLine[], cols: number): TerminalLink[] {
  const links: TerminalLink[] = [];
  for (const group of logicalLines(lines, cols)) {
    // The group's text, and for each character the line and cell it is in.
    let text = "";
    const where: TerminalPosition[] = [];
    for (const index of group) {
      const line = lines[index];
      // A program that broke a link itself pads the line it broke with
      // nothing; a soft wrap keeps every cell.
      const lineText = line.wrapped ? line.text : line.text.trimEnd();
      for (let i = 0; i < lineText.length; i += 1) {
        text += lineText[i];
        where.push({line: index, column: line.columns?.[i] ?? i});
      }
    }
    for (const match of text.matchAll(urlStart)) {
      const start = match.index ?? 0;
      const candidate = text.slice(start).match(urlChars)?.[0] ?? "";
      const url = candidate.replace(trailing, "");
      // An ellipsis means the output cut the link short.
      if (text.slice(start + url.length, start + url.length + 1) === "…") continue;
      try {
        const parsed = new URL(url);
        if (parsed.protocol !== "http:" && parsed.protocol !== "https:") continue;
      } catch {
        continue;
      }
      links.push({url, start: where[start], end: where[start + url.length - 1]});
    }
  }
  return links;
}

// The text of a link to show where there is little room: its host and
// path, cut to max characters.
export function shortLink(url: string, max = 48): string {
  let text = url;
  try {
    const parsed = new URL(url);
    text = parsed.host + parsed.pathname + (parsed.search ? "?…" : "");
  } catch {
    // Show it as it is.
  }
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}
