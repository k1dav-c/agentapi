/**
 * Product copy lives here so adding a locale does not require searching
 * through layout components. Keep the default locale aligned with <html lang>.
 */
export const defaultLocale = "en";

export const uiCopy = {
  metadata: {
    title: "AgentAPI — Live Agent Session",
    description: "Chat with and control your remote coding agent.",
  },
  tools: {
    groupLabel: (count: number) => `${count} consecutive tool calls`,
    expandAll: "Expand all",
    collapseAll: "Collapse all",
  },
  taskToolbar: {
    title: "Task tools",
    all: "All tasks",
    running: "Running",
    queued: "Queued",
    failed: "Failed",
    completed: "Completed",
    toolErrors: "Tool errors",
    download: "Download conversation",
  },
} as const;
