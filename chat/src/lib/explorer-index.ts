// What the Explorer lists from a conversation: the links and absolute file
// paths in its messages, each with the task it first appeared in. What a
// message holds is found once per message: while the agent works only its
// last message changes, and the rest come from the cache.

interface IndexedMessage {
  role: string;
  content: string;
}

// Absolute file paths in message text, also in Markdown code (`/a/b.go`) and
// at the end of a sentence.
export const pathPattern = /(?:^|[\s"'(`])((?:\/[\w.@+-]+)+\.[a-zA-Z0-9]{1,10})(?=$|[\s"'),:;`.])/g;

export interface DiscoveredLink {
  url: string;
  task: number;
}

export interface DiscoveredFile {
  path: string;
  sourceTask: number;
}

const linksOf = new WeakMap<IndexedMessage, string[]>();
const pathsOf = new WeakMap<IndexedMessage, string[]>();

// found returns what find finds in message's content, from the cache when
// the message was looked at before.
function found(cache: WeakMap<IndexedMessage, string[]>, message: IndexedMessage, find: (content: string) => string[]): string[] {
  let items = cache.get(message);
  if (!items) {
    items = find(message.content);
    cache.set(message, items);
  }
  return items;
}

// firstSeen lists each item of the messages once, with the task it first
// appears in (counted by user messages; 0 before the first).
function firstSeen(messages: IndexedMessage[], itemsOf: (message: IndexedMessage) => string[]): [string, number][] {
  const seen = new Map<string, number>();
  let task = 0;
  for (const message of messages) {
    if (message.role === "user") task += 1;
    for (const item of itemsOf(message)) {
      if (!seen.has(item)) seen.set(item, task);
    }
  }
  return [...seen];
}

export function discoverLinks(messages: IndexedMessage[]): DiscoveredLink[] {
  return firstSeen(messages, (message) => found(linksOf, message, reconstructWrappedURLs)).map(([url, task]) => ({url, task}));
}

export function discoverFiles(messages: IndexedMessage[]): DiscoveredFile[] {
  return firstSeen(messages, (message) => found(pathsOf, message, findPaths)).map(([path, sourceTask]) => ({path, sourceTask}));
}

function findPaths(content: string): string[] {
  return [...content.matchAll(pathPattern)].map((match) => match[1]);
}

// The http(s) URLs in content, also ones a terminal wrapped onto the next
// lines.
export function reconstructWrappedURLs(content: string) {
  const lines = content.split("\n");
  const urls: string[] = [];
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const line = lines[lineIndex];
    const starts = [...line.matchAll(/https?:\/\//g)].map((match) => match.index ?? 0);
    for (const start of starts) {
      let candidate = line.slice(start).match(/^\S+/)?.[0] ?? "";
      const reachesLineEnd = start + candidate.length === line.trimEnd().length;
      let next = lineIndex + 1;
      while (
        reachesLineEnd &&
        next < lines.length &&
        (lines[next - 1].trimEnd().length >= 72 ||
          /[/?#&=._~%+-]$/.test(candidate)) &&
        candidate.length > 0 &&
        /^[A-Za-z0-9/?#&=._~%+:@!,;()[\]-]+$/.test(lines[next].trim()) &&
        !/\s/.test(lines[next].trim())
      ) {
        candidate += lines[next].trim();
        next += 1;
      }
      candidate = candidate.replace(/[),.;:!?]+$/u, "");
      try {
        const parsed = new URL(candidate);
        if (parsed.protocol === "http:" || parsed.protocol === "https:") {
          urls.push(parsed.toString());
        }
      } catch {
        // Ignore text that only looked like a URL.
      }
    }
  }
  return [...new Set(urls)];
}
