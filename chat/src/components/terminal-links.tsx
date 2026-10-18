"use client";

import {useState} from "react";
import {Check, Copy, ExternalLink} from "lucide-react";
import type {IBufferLine, ILink, ILinkProvider, Terminal} from "@xterm/xterm";
import {findTerminalLinks, shortLink, type TerminalLine} from "@/lib/terminal-links";
import {copyText} from "@/lib/clipboard";

// How many lines around a line are read to find a link that runs over it.
const AROUND = 20;

// lineOf reads a buffer line cell by cell, so a link's characters map to
// the cells they take, also after wide characters.
function lineOf(line: IBufferLine): TerminalLine {
  let text = "";
  const columns: number[] = [];
  for (let x = 0; x < line.length; x += 1) {
    const cell = line.getCell(x);
    // The second half of a wide character has width 0.
    if (!cell || cell.getWidth() === 0) continue;
    const chars = cell.getChars() || " ";
    for (const char of chars) {
      text += char;
      columns.push(x);
    }
  }
  return {text, wrapped: line.isWrapped, columns};
}

// linesBetween reads the buffer's lines first to last (0-based) and returns
// them with the index of the first.
function linesBetween(term: Terminal, first: number, last: number): TerminalLine[] {
  const buffer = term.buffer.active;
  const lines: TerminalLine[] = [];
  for (let y = first; y <= last; y += 1) {
    const line = buffer.getLine(y);
    lines.push(line ? lineOf(line) : {text: "", wrapped: false});
  }
  return lines;
}

export function openLink(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}

// linkProvider makes the web links in the terminal clickable, also ones
// the terminal wrapped or the program broke over several lines.
export function linkProvider(term: Terminal): ILinkProvider {
  return {
    provideLinks(bufferLineNumber, callback) {
      const y = bufferLineNumber - 1;
      const first = Math.max(0, y - AROUND);
      const last = Math.min(term.buffer.active.length - 1, y + AROUND);
      const links: ILink[] = findTerminalLinks(linesBetween(term, first, last), term.cols)
        .filter((link) => link.start.line + first <= y && link.end.line + first >= y)
        .map((link) => ({
          text: link.url,
          range: {
            start: {x: link.start.column + 1, y: link.start.line + first + 1},
            end: {x: link.end.column + 1, y: link.end.line + first + 1},
          },
          activate: (_event, text) => openLink(text),
        }));
      callback(links.length > 0 ? links : undefined);
    },
  };
}

// latestLink returns the last web link in the terminal's last lines, where
// a command just printed it (a login URL), or null.
export function latestLink(term: Terminal, lines = 40): string | null {
  const buffer = term.buffer.active;
  const last = buffer.length - 1;
  const links = findTerminalLinks(linesBetween(term, Math.max(0, last - lines + 1), last), term.cols);
  return links.length > 0 ? links[links.length - 1].url : null;
}

// LatestLinkChip shows the link a command in the terminal printed last, to
// open or copy with one tap: a long link wraps over lines and is hard to hit
// in the terminal, above all on a phone.
export function LatestLinkChip({url}: {url: string | null}) {
  const [copied, setCopied] = useState(false);
  if (!url) return null;
  const copy = async () => {
    try {
      await copyText(url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // The link stays there to open.
    }
  };
  return (
    // On a phone it takes a row of its own under the title and Back to chat.
    <span className="order-last flex min-w-0 basis-full items-center gap-1 rounded-md border bg-muted/50 py-0.5 pr-0.5 pl-2 sm:order-none sm:basis-auto" data-terminal-link>
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        title={url}
        className="flex min-w-0 items-center gap-1.5 font-mono text-[11px] text-foreground underline-offset-2 hover:underline"
      >
        <ExternalLink className="size-3.5 shrink-0" />
        <span className="truncate">{shortLink(url)}</span>
      </a>
      <button
        type="button"
        onClick={() => void copy()}
        className="inline-flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
        title="Copy the link"
        aria-label="Copy the link"
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </span>
  );
}
