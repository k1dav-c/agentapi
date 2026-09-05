export function formatToolInput(input: unknown): string {
  if (input === undefined || input === null) return "";
  if (typeof input === "string") {
    try {
      return JSON.stringify(JSON.parse(input), null, 2);
    } catch {
      return input;
    }
  }

  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

export function getToolSummary(_name: string, input: unknown): string {
  if (!input || typeof input !== "object") return "";
  const obj = input as Record<string, unknown>;
  // File operations — show path
  const filePath = obj.file_path ?? obj.path ?? obj.file ?? obj.notebook_path;
  if (typeof filePath === "string") return filePath.replace(/^.*\//, "");
  // Bash — show command (truncated)
  if (typeof obj.command === "string") {
    const cmd = obj.command.split("\n")[0].trim();
    return cmd.length > 60 ? `${cmd.slice(0, 57)}…` : cmd;
  }
  // Search — show pattern or query
  const pattern = obj.pattern ?? obj.regex ?? obj.query;
  if (typeof pattern === "string") {
    return pattern.length > 50 ? `${pattern.slice(0, 47)}…` : pattern;
  }
  // URL fetch
  if (typeof obj.url === "string") {
    try {
      return new URL(obj.url).hostname;
    } catch {
      return "";
    }
  }
  return "";
}
