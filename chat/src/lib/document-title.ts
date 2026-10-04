import type {
  ConnectionStatus,
  ServerStatus,
} from "@/components/chat-provider";
import { getAgentState } from "./session-status";

const DEFAULT_TITLE = "AgentAPI — Live Agent Session";
const MAX_TASK_LENGTH = 60;

function summarizeTask(content: string) {
  const normalized = content.replace(/\s+/g, " ").trim();
  if (normalized.length <= MAX_TASK_LENGTH) return normalized;
  return `${normalized.slice(0, MAX_TASK_LENGTH - 1).trimEnd()}…`;
}

export function getDocumentTitle({
  connectionStatus,
  serverStatus,
  terminalPrompt,
  task,
  customTitle,
}: {
  connectionStatus: ConnectionStatus;
  serverStatus: ServerStatus;
  terminalPrompt?: string;
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
  }[getAgentState(serverStatus, connectionStatus, terminalPrompt)];
  const taskSummary = task ? summarizeTask(task) : "";
  const suffix = customTitle || "AgentAPI";

  return taskSummary
    ? `${status} · ${taskSummary} — ${suffix}`
    : `${status} · ${customTitle || DEFAULT_TITLE}`;
}
