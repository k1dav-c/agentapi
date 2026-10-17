// File paths in the chat, for the file viewer: paths the agent writes in
// its messages, and the files a tool call reads or changes.

import {deleteDiff, diffHeader, editDiff, writeDiff, type TextEdit} from "./diff";

export interface FilePathRef {
  // As written: absolute, or relative to the agent's working directory.
  path: string;
  line?: number;
  col?: number;
}

const MAX_PATH_LENGTH = 300;
// A trailing :line or :line:col, as compilers and agents write it.
const LINE_SUFFIX = /:(\d{1,7})(?::(\d{1,5}))?$/;
// Letters and digits of any script, and the punctuation file names use
// (brackets and parentheses for routes like app/[id]/page.tsx).
const PATH_CHARS = /^[\p{L}\p{M}\p{N}_.@+\-/[\]()]+$/u;
// name.ext, where the extension starts with a letter: not 1.2.3 or v0.23.0.
const EXTENSION = /^(.*[^.])\.([A-Za-z][A-Za-z0-9]{0,11})$/;
const HAS_ALPHANUMERIC = /[\p{L}\p{N}]/u;
// A first segment that is a host: github.com/coder/agentapi/main.go is a
// URL without its scheme, not a file.
const HOST = /^[\w-]+(?:\.[\w-]+)*\.(?:com|org|net|io|dev|app|ai|co|gov|edu|sh|me|so|xyz|info|cn|tw|jp|de|uk|ru|fr|us)$/i;

// Extensions a file name without a directory needs to count as a file:
// without the list, React.memo, fmt.Println or window.location would too.
const FILE_EXTENSIONS = new Set([
  // Code
  "c", "h", "cc", "cpp", "cxx", "hpp", "hh", "hxx", "cs", "go", "rs", "py", "pyi", "rb", "java", "kt", "kts",
  "scala", "swift", "js", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts", "vue", "svelte", "astro", "php", "lua",
  "pl", "dart", "ex", "exs", "erl", "hs", "clj", "cljs", "elm", "zig", "nim", "jl", "sol", "groovy", "gradle",
  "sql", "graphql", "gql", "proto",
  // Web
  "html", "htm", "css", "scss", "sass", "less", "svg",
  // Configuration and data
  "json", "jsonc", "json5", "jsonl", "ndjson", "yaml", "yml", "toml", "ini", "cfg", "conf", "xml", "plist",
  "properties", "lock", "mod", "sum", "csv", "tsv", "tf", "tfvars", "hcl", "nix", "ipynb",
  // Documents and logs
  "md", "mdx", "markdown", "rst", "txt", "adoc", "tex", "pdf", "log",
  // Scripts and builds
  "sh", "bash", "zsh", "fish", "ps1", "bat", "mk", "cmake", "dockerfile",
  // Images
  "png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "avif",
  // Patches
  "patch", "diff",
]);

// Files without an extension that are files all the same.
const EXTENSIONLESS_FILES = new Set([
  "Makefile", "GNUmakefile", "Dockerfile", "Containerfile", "Justfile", "Gemfile", "Rakefile", "Procfile",
  "Jenkinsfile", "Vagrantfile", "Brewfile", "LICENSE", "CODEOWNERS",
]);

const DOTFILE =
  /^\.(?:env(?:\.[\w-]+)*|gitignore|gitattributes|gitmodules|dockerignore|npmrc|nvmrc|editorconfig|prettierrc|eslintrc|babelrc|bashrc|zshrc|profile|bash_profile|tool-versions)$/;

// Names that look like files but are code or products (compared in lower
// case).
const NOT_FILES = new Set([
  "console.log", "res.json", "req.json", "resp.json", "response.json", "request.json",
  "node.js", "next.js", "nuxt.js", "vue.js", "react.js", "express.js", "nest.js", "three.js", "d3.js", "chart.js",
  "ember.js", "angular.js", "backbone.js", "deno.js",
]);

function isFileName(name: string, bare: boolean): boolean {
  if (EXTENSIONLESS_FILES.has(name) || DOTFILE.test(name)) return true;
  if (bare && NOT_FILES.has(name.toLowerCase())) return false;
  const match = EXTENSION.exec(name);
  if (!match || !HAS_ALPHANUMERIC.test(match[1])) return false;
  return !bare || FILE_EXTENSIONS.has(match[2].toLowerCase());
}

// Whether text (inline code in a message, say) is a file path, and the file
// and line it names. Conservative, since whatever it accepts becomes a
// link: a path has no spaces and names a file (with an extension, or one
// of a few known names like Makefile), and a bare file name needs a known
// extension. URLs, versions (1.2.3, v0.23.0), flags (-v), abbreviations
// (e.g.), module names (next/navigation), directories and ~ paths (the
// server doesn't expand ~) are not.
export function parseFilePathRef(text: string): FilePathRef | null {
  if (!text || text.length > MAX_PATH_LENGTH || /\s/.test(text)) return null;
  let path = text;
  let line: number | undefined;
  let col: number | undefined;
  const suffix = LINE_SUFFIX.exec(text);
  if (suffix) {
    path = text.slice(0, suffix.index);
    line = Number(suffix[1]) || undefined;
    col = line && suffix[2] ? Number(suffix[2]) || undefined : undefined;
  }
  if (!PATH_CHARS.test(path) || path.startsWith("-") || path.startsWith("@/") || path.includes("//")) return null;
  const segments = path.split("/");
  // Only an absolute path starts with an empty segment; a trailing slash is
  // a directory.
  if (segments.some((segment, index) => (segment === "" && index > 0) || /^\.{3,}$/.test(segment))) return null;
  const name = segments[segments.length - 1];
  if (!isFileName(name, segments.length === 1)) return null;
  if (segments.length > 1 && HOST.test(segments[0])) return null;
  return {path, ...(line ? {line} : {}), ...(col ? {col} : {})};
}

// The last segment of a path.
export function fileName(path: string): string {
  return path.replace(/\/+$/, "").split("/").pop() || path;
}

export type FileTargetKind = "read" | "write" | "edit" | "delete" | "image";

// A file a tool call reads or changes.
export interface FileTarget {
  // As the tool has it: absolute, or relative to the agent's working
  // directory. For a moved file, where it is now.
  path: string;
  kind: FileTargetKind;
  // The line a read starts at.
  line?: number;
  // Where a moved file was.
  movedFrom?: string;
  // This tool call's own change to the file as a unified diff, when the
  // input has it.
  change?: string;
}

// The tab of the file viewer a target opens on: the change a tool call made,
// or the file it read.
export function viewerTab(kind: FileTargetKind): "file" | "diff" {
  return kind === "write" || kind === "edit" || kind === "delete" ? "diff" : "file";
}

type Fields = Record<string, unknown>;

function isFields(value: unknown): value is Fields {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function stringField(fields: Fields | undefined, ...names: string[]): string | undefined {
  for (const name of names) {
    const value = fields?.[name];
    if (typeof value === "string" && value) return value;
  }
  return undefined;
}

// A tool input that arrived as JSON text is read as the object it holds.
function parseToolInput(input: unknown): unknown {
  if (typeof input !== "string" || !input.trimStart().startsWith("{")) return input;
  try {
    const parsed: unknown = JSON.parse(input);
    return isFields(parsed) ? parsed : input;
  } catch {
    return input;
  }
}

// Claude Code's Edit ({old_string, new_string}) and MultiEdit ({edits}), and
// Pi's edit ({edits: [{oldText, newText}]}, or one edit inline).
function textEdits(fields: Fields): TextEdit[] {
  const edits: unknown[] = Array.isArray(fields.edits) ? fields.edits : [fields];
  return edits.flatMap((edit) => {
    if (!isFields(edit)) return [];
    const oldText = edit.old_string ?? edit.oldText;
    const newText = edit.new_string ?? edit.newText;
    return typeof oldText === "string" && typeof newText === "string" ? [{oldText, newText}] : [];
  });
}

// A JavaScript string literal's value.
function unquote(literal: string): string {
  return literal.slice(1, -1).replace(/\\(u\{[0-9a-fA-F]+\}|u[0-9a-fA-F]{4}|x[0-9a-fA-F]{2}|\r\n|[\s\S])/g, (_, escape: string) => {
    if (escape.length > 1 && (escape[0] === "u" || escape[0] === "x")) {
      const code = parseInt(escape[1] === "{" ? escape.slice(2, -1) : escape.slice(1), 16);
      return code <= 0x10ffff ? String.fromCodePoint(code) : "";
    }
    switch (escape) {
      case "n": return "\n";
      case "r": return "\r";
      case "t": return "\t";
      case "b": return "\b";
      case "f": return "\f";
      case "v": return "\v";
      case "0": return "\0";
      // A line continuation.
      case "\r\n": case "\n": case "\r": case "\u2028": case "\u2029": return "";
      default: return escape;
    }
  });
}

const BEGIN_PATCH = "*** Begin Patch";
const END_PATCH = "*** End Patch";
// tools.apply_patch("…") in a Codex code-mode script, also with the patch in
// an object ({patch: "…"}).
const PATCH_CALL =
  /apply_patch\(\s*(?:\{\s*["']?(?:patch|input)["']?\s*:\s*)?("(?:[^"\\]|\\[\s\S])*"|'(?:[^'\\]|\\[\s\S])*'|`(?:[^`\\]|\\[\s\S])*`)/g;

// The apply_patch patches in source: the patch itself, a patch in a shell
// heredoc, or one passed to tools.apply_patch in a script.
function extractPatches(source: string): string[] {
  if (!source.includes(BEGIN_PATCH)) return [];
  const fromCalls = [...source.matchAll(PATCH_CALL)]
    .map((match) => unquote(match[1]))
    .filter((patch) => patch.includes(BEGIN_PATCH));
  if (fromCalls.length > 0) return fromCalls;
  const patches: string[] = [];
  let start = source.indexOf(BEGIN_PATCH);
  while (start >= 0) {
    const end = source.indexOf(END_PATCH, start);
    patches.push(source.slice(start, end < 0 ? undefined : end + END_PATCH.length));
    start = end < 0 ? -1 : source.indexOf(BEGIN_PATCH, end);
  }
  return patches;
}

const PATCH_FILE = /^\*\*\* (Add|Delete|Update) File: (.+)$/;
const PATCH_MOVE = /^\*\*\* Move to: (.+)$/;

// The files of a Codex apply_patch patch ("*** Update File: <path>", then
// its hunks), each with its change as a unified diff.
function parsePatch(patch: string): FileTarget[] {
  const sections: {op: string; path: string; moveTo?: string; lines: string[]}[] = [];
  for (const line of patch.split(/\r?\n/)) {
    if (line.startsWith(END_PATCH)) break;
    const file = PATCH_FILE.exec(line);
    if (file) {
      sections.push({op: file[1], path: file[2].trim(), lines: []});
      continue;
    }
    const section = sections.at(-1);
    if (!section) continue;
    const move = PATCH_MOVE.exec(line);
    if (move) section.moveTo = move[1].trim();
    // Other markers (*** End of File) aren't part of the change.
    else if (!line.startsWith("*** ")) section.lines.push(line);
  }
  return sections.map(({op, path, moveTo, lines}): FileTarget => {
    while (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
    if (op === "Add") {
      const added = lines.filter((line) => line !== "").map((line) => (line.startsWith("+") ? line : `+${line}`));
      const hunk = added.length > 0 ? [`@@ -0,0 +1,${added.length} @@`, ...added] : [];
      return {path, kind: "write", change: [diffHeader(null, path), ...hunk].join("\n")};
    }
    if (op === "Delete") return {path, kind: "delete", change: diffHeader(path, null)};
    const to = moveTo ?? path;
    const hunks = lines.length > 0 && !lines[0].startsWith("@@") ? ["@@", ...lines] : lines;
    return {path: to, kind: "edit", ...(moveTo ? {movedFrom: path} : {}), change: [diffHeader(path, to), ...hunks].join("\n")};
  });
}

function patchTargets(sources: unknown[]): FileTarget[] {
  return sources.flatMap((source) => (typeof source === "string" ? extractPatches(source) : [])).flatMap(parsePatch);
}

const HAS_HUNKS = /^@@/m;

// The changes of Codex's native apply_patch ({changes: [{path, kind, diff,
// move_path}]}). kind is "add", "delete" or "update" (or an object with a
// type and move_path, as app-server sends it). An update's diff is its
// hunks, ending in "Moved to: <path>" for a moved file; an added or
// deleted file's diff is its content.
function codexChanges(changes: unknown[]): FileTarget[] {
  return changes.flatMap((change): FileTarget[] => {
    if (!isFields(change)) return [];
    const path = stringField(change, "path");
    if (!path) return [];
    const kind: Fields = isFields(change.kind) ? change.kind : {type: change.kind};
    const type = stringField(kind, "type") ?? "update";
    let diff = typeof change.diff === "string" ? change.diff : "";
    let moveTo = stringField(change, "move_path") ?? stringField(kind, "move_path");
    const moved = /\n*Moved to: ([^\n]+)\n*$/.exec(diff);
    if (moved && type === "update") {
      diff = diff.slice(0, moved.index);
      moveTo ??= moved[1].trim();
    }
    const hunks = diff.replace(/\n+$/, "");
    if (type === "add") {
      return [{path, kind: "write", change: HAS_HUNKS.test(diff) ? `${diffHeader(null, path)}\n${hunks}` : writeDiff(path, diff, true)}];
    }
    if (type === "delete") {
      return [{path, kind: "delete", change: HAS_HUNKS.test(diff) ? `${diffHeader(path, null)}\n${hunks}` : deleteDiff(path, diff)}];
    }
    const to = moveTo ?? path;
    return [{
      path: to,
      kind: "edit",
      ...(moveTo ? {movedFrom: path} : {}),
      change: hunks ? `${diffHeader(path, to)}\n${hunks}` : diffHeader(path, to),
    }];
  });
}

// The shell or script of a Codex command tool, where a patch may be.
function scriptsOf(input: unknown): unknown[] {
  if (typeof input === "string") return [input];
  if (!isFields(input)) return [];
  const command = input.command ?? input.cmd;
  return [input.script, Array.isArray(command) ? command.filter((part) => typeof part === "string").join(" ") : command];
}

// The files a tool call reads or changes, with its change to each when the
// input has it:
//   - Claude Code: Read {file_path, offset}, Write {file_path, content}, Edit
//     {file_path, old_string, new_string}, MultiEdit {file_path, edits} and
//     NotebookEdit {notebook_path}
//   - Pi: read {path, offset}, write {path, content} and edit {path, edits}
//   - Codex: apply_patch, natively {changes} or as the patch text (also from
//     a code-mode exec script or the shell), and view_image {path}
export function toolFileTargets(toolName: string, toolInput: unknown): FileTarget[] {
  const input = parseToolInput(toolInput);
  const fields = isFields(input) ? input : undefined;
  const name = toolName.toLowerCase();
  switch (name) {
    case "read": {
      const path = stringField(fields, "file_path", "path");
      if (!path) return [];
      const offset = fields?.offset;
      return [{path, kind: "read", ...(typeof offset === "number" && offset >= 1 ? {line: Math.floor(offset)} : {})}];
    }
    case "write": {
      const path = stringField(fields, "file_path", "path");
      if (!path) return [];
      const content = fields?.content;
      return [{path, kind: "write", ...(typeof content === "string" ? {change: writeDiff(path, content)} : {})}];
    }
    case "edit":
    case "multiedit": {
      const path = stringField(fields, "file_path", "path");
      if (!path || !fields) return [];
      const edits = textEdits(fields);
      return [{path, kind: "edit", ...(edits.length > 0 ? {change: editDiff(path, edits)} : {})}];
    }
    case "notebookedit": {
      const path = stringField(fields, "notebook_path");
      return path ? [{path, kind: "edit"}] : [];
    }
    case "view_image": {
      const path = stringField(fields, "path");
      return path ? [{path, kind: "image"}] : [];
    }
    case "apply_patch":
      if (fields && Array.isArray(fields.changes)) return codexChanges(fields.changes);
      return patchTargets(fields ? [fields.input, fields.patch] : [input]);
  }
  // Codex applies patches from code-mode scripts (exec) and, in older
  // versions, through the shell.
  return /^(?:exec|shell|local_shell|container\.exec)/.test(name) ? patchTargets(scriptsOf(input)) : [];
}
