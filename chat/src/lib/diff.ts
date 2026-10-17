// Unified diffs for the file viewer and tool calls: built from what a tool
// call changed, and split into classified lines to render.

export type DiffLineKind = "header" | "hunk" | "add" | "del" | "context" | "meta";

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
}

// The lines of a text, without the empty one after a final newline.
export function textLines(text: string): string[] {
  if (!text) return [];
  const lines = text.split(/\r?\n/);
  if (lines.at(-1) === "") lines.pop();
  return lines;
}

// One side of a file header as git writes it: a/<path>, also for an
// absolute path (git diff --no-index writes a/tmp/x for /tmp/x), or
// /dev/null for a file that doesn't exist on that side.
function headerPath(prefix: "a" | "b", path: string | null): string {
  return path === null ? "/dev/null" : `${prefix}/${path.replace(/^\/+/, "")}`;
}

// The ---/+++ header of a file's diff; null for a side without the file.
export function diffHeader(from: string | null, to: string | null): string {
  return `--- ${headerPath("a", from)}\n+++ ${headerPath("b", to)}`;
}

// Edits bigger than this many line pairs show every old line removed and
// every new line added instead of a line diff.
const MAX_DIFF_CELLS = 250_000;

// The lines of a hunk that turns oldText into newText: lines both have as
// context (" "), and within each change the removed lines ("-") before the
// added ones ("+").
export function diffLines(oldText: string, newText: string): string[] {
  const a = textLines(oldText);
  const b = textLines(newText);
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const changedA = a.slice(start, endA);
  const changedB = b.slice(start, endB);
  const middle =
    changedA.length * changedB.length > MAX_DIFF_CELLS
      ? [...changedA.map((line) => `-${line}`), ...changedB.map((line) => `+${line}`)]
      : lcsDiff(changedA, changedB);
  return [
    ...a.slice(0, start).map((line) => ` ${line}`),
    ...middle,
    ...a.slice(endA).map((line) => ` ${line}`),
  ];
}

// A line diff through the longest common subsequence of a and b.
function lcsDiff(a: string[], b: string[]): string[] {
  const n = a.length;
  const m = b.length;
  const width = m + 1;
  // common[i * width + j]: length of the longest common subsequence of
  // a[i:] and b[j:].
  const common = new Uint32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      common[i * width + j] =
        a[i] === b[j]
          ? common[(i + 1) * width + j + 1] + 1
          : Math.max(common[(i + 1) * width + j], common[i * width + j + 1]);
    }
  }
  const lines: string[] = [];
  let removed: string[] = [];
  let added: string[] = [];
  const flush = () => {
    lines.push(...removed, ...added);
    removed = [];
    added = [];
  };
  let i = 0;
  let j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && a[i] === b[j]) {
      flush();
      lines.push(` ${a[i]}`);
      i++;
      j++;
    } else if (j >= m || (i < n && common[(i + 1) * width + j] >= common[i * width + j + 1])) {
      removed.push(`-${a[i]}`);
      i++;
    } else {
      added.push(`+${b[j]}`);
      j++;
    }
  }
  flush();
  return lines;
}

export interface TextEdit {
  oldText: string;
  newText: string;
}

// The diff of edits that each replace oldText with newText in path, one
// hunk per edit. A tool input doesn't say where in the file an edit is, so
// the hunks have no line numbers.
export function editDiff(path: string, edits: TextEdit[]): string {
  const hunks = edits.map(({oldText, newText}) => ["@@", ...diffLines(oldText, newText)].join("\n"));
  return [diffHeader(path, path), ...hunks].join("\n");
}

// The diff of writing content to path: every line added. created says the
// file is new; otherwise it may have existed.
export function writeDiff(path: string, content: string, created = false): string {
  const lines = textLines(content).map((line) => `+${line}`);
  const hunk = lines.length > 0 ? [created ? `@@ -0,0 +1,${lines.length} @@` : "@@", ...lines] : [];
  return [diffHeader(created ? null : path, path), ...hunk].join("\n");
}

// The diff of deleting path, with its lines when they are known.
export function deleteDiff(path: string, content = ""): string {
  const lines = textLines(content).map((line) => `-${line}`);
  const hunk = lines.length > 0 ? [`@@ -1,${lines.length} +0,0 @@`, ...lines] : [];
  return [diffHeader(path, null), ...hunk].join("\n");
}

const HUNK_HEADER = /^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@/;
const FILE_HEADER =
  /^(?:diff --git |index [0-9a-f]|(?:new|deleted) file mode |old mode |new mode |(?:dis)?similarity index |rename (?:from|to) |copy (?:from|to) |Binary files |--- |\+\+\+ )/;

// Classifies the lines of a unified diff: file headers, hunk headers, added,
// removed and unchanged lines, and anything else (a "\ No newline at end of
// file", text around the diff). A hunk whose header gives line counts (git)
// ends after that many lines; one without (a diff built from a tool call)
// ends at the next file's ---/+++ header.
export function classifyDiffLines(diff: string): DiffLine[] {
  const lines = textLines(diff);
  const result: DiffLine[] = [];
  let inHunk = false;
  // Old and new lines left in the hunk, when its header says.
  let oldLeft: number | null = null;
  let newLeft: number | null = null;
  for (let index = 0; index < lines.length; index++) {
    const text = lines[index];
    if (text.startsWith("@@")) {
      const counts = HUNK_HEADER.exec(text);
      inHunk = true;
      oldLeft = counts ? Number(counts[1] ?? 1) : null;
      newLeft = counts ? Number(counts[2] ?? 1) : null;
      result.push({kind: "hunk", text});
      continue;
    }
    if (inHunk) {
      const counted = oldLeft !== null && newLeft !== null;
      const ended = counted
        ? oldLeft! <= 0 && newLeft! <= 0
        : text.startsWith("diff --git ") || (text.startsWith("--- ") && Boolean(lines[index + 1]?.startsWith("+++ ")));
      if (!ended) {
        const marker = text[0];
        if (marker === "+" || marker === "-" || marker === " " || text === "") {
          if (counted) {
            if (marker !== "+") oldLeft!--;
            if (marker !== "-") newLeft!--;
          }
          result.push({kind: marker === "+" ? "add" : marker === "-" ? "del" : "context", text});
          continue;
        }
        if (marker === "\\") {
          result.push({kind: "meta", text});
          continue;
        }
      }
      inHunk = false;
    }
    result.push({kind: FILE_HEADER.test(text) ? "header" : "meta", text});
  }
  return result;
}

// How many lines a diff adds and removes.
export function diffStats(lines: DiffLine[]): {added: number; removed: number} {
  let added = 0;
  let removed = 0;
  for (const line of lines) {
    if (line.kind === "add") added++;
    else if (line.kind === "del") removed++;
  }
  return {added, removed};
}
