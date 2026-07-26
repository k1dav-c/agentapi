export interface SearchableTask {
  prompt: string;
  responses: string[];
  tools: Array<{name: string; input?: string; result?: string; isError?: boolean}>;
}

export function taskMatchesQuery(task: SearchableTask, query: string) {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return true;
  return [
    task.prompt,
    ...task.responses,
    ...task.tools.flatMap((tool) => [
      tool.name,
      tool.input ?? "",
      tool.result ?? "",
    ]),
  ].some((content) => content.toLocaleLowerCase().includes(normalized));
}

export function taskToMarkdown(task: SearchableTask, number: number) {
  const sections = [`# Task ${number}`, "", "## Prompt", "", task.prompt];
  if (task.responses.length > 0) {
    sections.push("", "## Agent output", "", ...task.responses);
  }
  for (const tool of task.tools) {
    sections.push(
      "",
      `## Tool: ${tool.name}${tool.isError ? " (failed)" : ""}`,
    );
    if (tool.input) sections.push("", "### Input", "", "```json", tool.input, "```");
    if (tool.result !== undefined) {
      sections.push("", "### Result", "", "```text", tool.result, "```");
    }
  }
  return `${sections.join("\n")}\n`;
}
