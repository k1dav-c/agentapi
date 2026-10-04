// Splits a thinking block into its title and body. Claude and Codex start
// reasoning summaries with a bold heading ("**Listing test files**").
export function splitThinking(content: string): { title: string; body: string } {
  const trimmed = content.trim();
  const match = trimmed.match(/^\*\*(.+?)\*\*\s*\n?/);
  if (match) return { title: match[1].trim(), body: trimmed.slice(match[0].length).trim() };
  const firstLine = trimmed.split("\n")[0];
  return { title: firstLine.length > 80 ? `${firstLine.slice(0, 77)}…` : firstLine, body: trimmed };
}
