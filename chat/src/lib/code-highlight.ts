// Syntax highlighting of a whole file, line by line, for the file viewer.

import {createLowlight} from "lowlight";
import {common} from "./highlight-langs";

const lowlight = createLowlight(common);

// Files longer than this are shown without highlighting, to stay fast.
export const MAX_HIGHLIGHT_CHARS = 200_000;

// Extensions lowlight doesn't know by name, mapped to a language it has.
const LANGUAGE_BY_EXTENSION: Record<string, string> = {
  mdx: "markdown",
  htm: "xml",
  vue: "xml",
  svelte: "xml",
  astro: "xml",
  jsonl: "json",
  ndjson: "json",
  json5: "json",
  ipynb: "json",
  pyi: "python",
  env: "bash",
};

// Files known by their name (compared in lower case).
const LANGUAGE_BY_NAME: Record<string, string> = {
  makefile: "makefile",
  gnumakefile: "makefile",
  gemfile: "ruby",
  rakefile: "ruby",
  brewfile: "ruby",
  vagrantfile: "ruby",
  ".bashrc": "bash",
  ".zshrc": "bash",
  ".profile": "bash",
  ".bash_profile": "bash",
};

// The highlight.js language for a file, from its name; undefined for plain
// text.
export function languageForPath(path: string): string | undefined {
  const name = (path.split("/").pop() ?? "").toLowerCase();
  if (LANGUAGE_BY_NAME[name]) return LANGUAGE_BY_NAME[name];
  if (/^\.env(?:\.|$)/.test(name)) return "bash";
  const extension = /\.([^.]+)$/.exec(name)?.[1];
  // txt and text are highlight.js's aliases of plain text.
  if (!extension || extension === "txt" || extension === "text") return undefined;
  if (LANGUAGE_BY_EXTENSION[extension]) return LANGUAGE_BY_EXTENSION[extension];
  return lowlight.registered(extension) ? extension : undefined;
}

// A run of text and the highlight.js classes it is nested in, outermost
// first.
export interface CodeToken {
  text: string;
  classes: string[];
}

export type CodeLine = CodeToken[];

interface HighlightNode {
  type: string;
  value?: string;
  properties?: {className?: unknown};
  children?: HighlightNode[];
}

// code as lines of tokens, highlighted as language (plain without one, for
// a language lowlight doesn't know, or past MAX_HIGHLIGHT_CHARS). A final
// newline doesn't start another line.
export function highlightCode(code: string, language: string | undefined): CodeLine[] {
  const text = code.replace(/\r\n/g, "\n");
  const lines: CodeLine[] = [[]];
  const add = (value: string, classes: string[]) => {
    value.split("\n").forEach((part, index) => {
      if (index > 0) lines.push([]);
      if (part) lines[lines.length - 1].push({text: part, classes});
    });
  };
  let tree: {children: unknown[]} | undefined;
  if (language && text.length <= MAX_HIGHLIGHT_CHARS) {
    try {
      tree = lowlight.highlight(language, text);
    } catch {
      // An unknown language: plain text.
    }
  }
  if (tree) {
    const walk = (nodes: HighlightNode[], classes: string[]) => {
      for (const node of nodes) {
        if (node.type === "text") {
          add(node.value ?? "", classes);
        } else if (node.type === "element") {
          const own = node.properties?.className;
          walk(node.children ?? [], Array.isArray(own) ? [...classes, ...own.map(String)] : classes);
        }
      }
    };
    walk(tree.children as HighlightNode[], []);
  } else {
    add(text, []);
  }
  if (lines.length > 1 && lines[lines.length - 1].length === 0) lines.pop();
  return lines;
}
