export interface InteractiveQuestion {
  question: string;
  header?: string;
  options: {label: string; description?: string}[];
  multiSelect?: boolean;
}

// Tools that ask the user multiple-choice questions: Claude Code's
// AskUserQuestion and Codex's request_user_input.
const QUESTION_TOOLS = new Set(["AskUserQuestion", "request_user_input"]);

// Parses an interactive tool's input into renderable questions. Codex sends
// tool input as a JSON-encoded string. ExitPlanMode has no options but is
// still a prompt awaiting the user.
export function parseInteractiveToolInput(
  name: string,
  rawInput: unknown,
): {questions: InteractiveQuestion[]} | null {
  let input = rawInput;
  if (typeof input === "string") {
    try {
      input = JSON.parse(input);
    } catch {
      return null;
    }
  }
  if (!input || typeof input !== "object") return null;
  const record = input as Record<string, unknown>;

  if (QUESTION_TOOLS.has(name) && Array.isArray(record.questions)) {
    return {questions: record.questions as InteractiveQuestion[]};
  }
  if (name === "ExitPlanMode") {
    return {questions: []};
  }
  return null;
}

// Index of the question the agent's prompt is currently asking, read from
// Codex's "Question 2/3" header in the agent output. null when the output
// has no such header (e.g. Claude Code, or the prompt has closed).
export function currentPromptQuestion(agentOutput: string): number | null {
  const matches = [...agentOutput.matchAll(/Question (\d+)\/(\d+)/g)];
  const last = matches[matches.length - 1];
  if (!last) return null;
  return Number(last[1]) - 1;
}
