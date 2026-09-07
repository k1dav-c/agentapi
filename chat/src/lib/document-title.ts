import type {
  ConnectionStatus,
  ServerStatus,
} from "@/components/chat-provider";

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
  task,
  customTitle,
}: {
  connectionStatus: ConnectionStatus;
  serverStatus: ServerStatus;
  task?: string;
  customTitle?: string;
}) {
  const status =
    connectionStatus === "offline"
      ? "○ Offline"
      : connectionStatus === "reconnecting"
        ? "↻ Reconnecting"
        : serverStatus === "running"
          ? "● Running"
          : serverStatus === "stable"
            ? "✓ Ready"
            : "○ Connecting";
  const taskSummary = task ? summarizeTask(task) : "";
  const suffix = customTitle || "AgentAPI";

  return taskSummary
    ? `${status} · ${taskSummary} — ${suffix}`
    : `${status} · ${customTitle || DEFAULT_TITLE}`;
}
