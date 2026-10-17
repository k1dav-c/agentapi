import type { AgentStateKind } from "./session-status";
import { uiCopy } from "./ui-copy";

const DEFAULT_TITLE = uiCopy.metadata.title;
const MAX_TASK_LENGTH = 60;

function summarizeTask(content: string) {
  const normalized = content.replace(/\s+/g, " ").trim();
  if (normalized.length <= MAX_TASK_LENGTH) return normalized;
  return `${normalized.slice(0, MAX_TASK_LENGTH - 1).trimEnd()}…`;
}

export function getDocumentTitle({
  agentState,
  task,
  customTitle,
}: {
  agentState: AgentStateKind;
  task?: string;
  customTitle?: string;
}) {
  // The tab title is how people notice an agent in a background tab, so it
  // leads with the state, and "Needs you" is the loudest.
  const status = {
    offline: "○ Offline",
    reconnecting: "↻ Reconnecting",
    "needs-you": "◆ Needs you",
    working: "● Working",
    ready: "✓ Ready",
    connecting: "○ Connecting",
  }[agentState];
  const taskSummary = task ? summarizeTask(task) : "";
  const suffix = customTitle || "AgentAPI";

  return taskSummary
    ? `${status} · ${taskSummary} — ${suffix}`
    : `${status} · ${customTitle || DEFAULT_TITLE}`;
}
