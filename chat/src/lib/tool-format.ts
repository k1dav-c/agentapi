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

export function getToolSummary(name: string, input: unknown): string {
  const command = getToolCommand(name, input);
  if (command) {
    const line = command.split("\n")[0].trim();
    return line.length > 60 ? `${line.slice(0, 57)}…` : line;
  }
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

// The shell command a tool ran, when it is a command tool: Claude's Bash
// ({command}), Codex's exec_command ({cmd}, string or argv) or freeform exec
// (the input is the script itself).
export function getToolCommand(name: string, input: unknown): string | undefined {
  if (typeof input === "string") {
    if (!/^(exec|shell|bash|run|local_shell)/i.test(name)) return undefined;
    return input.trim() || undefined;
  }
  if (!input || typeof input !== "object") return undefined;
  const obj = input as Record<string, unknown>;
  const command = obj.command ?? obj.cmd;
  if (typeof command === "string") return command.trim() || undefined;
  if (Array.isArray(command) && command.every((part) => typeof part === "string")) {
    // ["bash", "-lc", "pwd"] → the script after -lc
    const lc = command.indexOf("-lc");
    return (lc >= 0 && command[lc + 1] ? command[lc + 1] : command.join(" ")).trim() || undefined;
  }
  return undefined;
}

// Exit code reported in a command's output ("Exit code: 1",
// "Process exited with code 0"), if any.
export function getExitCode(result: string | undefined): number | undefined {
  const match = result?.match(/exit(?:ed with)? code:?\s*(-?\d+)/i);
  return match ? Number(match[1]) : undefined;
}
