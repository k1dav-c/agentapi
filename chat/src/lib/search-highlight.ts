// The parts of content around the matches of query (ignoring case), in
// order: what a search highlights. No query, or no match, is one part.
export function splitByQuery(content: string, query: string): {text: string; match: boolean}[] {
  const normalized = query.trim();
  if (!normalized) return [{text: content, match: false}];
  const expression = new RegExp(`(${normalized.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`, "giu");
  const lower = normalized.toLocaleLowerCase();
  return content
    .split(expression)
    .filter((text) => text !== "")
    .map((text) => ({text, match: text.toLocaleLowerCase() === lower}));
}
