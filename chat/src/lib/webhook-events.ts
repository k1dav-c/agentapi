// The webhook's events as the chat offers them: groups of event types, and
// the --webhook-events patterns (task.*, *) that select them.

export interface WebhookEventGroup {
  id: string;
  label: string;
  detail: string;
  // The event types in the group.
  events: string[];
  // The patterns that select it.
  patterns: string[];
}

export const WEBHOOK_EVENT_GROUPS: WebhookEventGroup[] = [
  {id: "tasks", label: "Tasks", detail: "Started, completed or failed, with the reply, duration and cost", events: ["task.started", "task.completed", "task.failed"], patterns: ["task.*"]},
  {
    id: "needs-you",
    label: "Needs you",
    detail: "Approvals, questions, plans and terminal dialogs; logins; usage limits",
    events: ["request.opened", "request.closed", "login.required", "login.completed", "limit.stopped", "limit.cleared"],
    patterns: ["request.*", "login.*", "limit.*"],
  },
  {id: "instance", label: "Heartbeats", detail: "When AgentAPI starts and stops, and what the agent does every interval", events: ["instance.started", "instance.heartbeat", "instance.stopped"], patterns: ["instance.*"]},
  {id: "status", label: "Run status", detail: "The agent changes between running and stable", events: ["run.status_changed"], patterns: ["run.status_changed"]},
  {id: "errors", label: "Agent errors", detail: "Errors and warnings the agent reports", events: ["agent.error"], patterns: ["agent.error"]},
  {id: "messages", label: "Messages", detail: "Every message of yours and the agent's final ones", events: ["message.created"], patterns: ["message.created"]},
  {id: "tools", label: "Tool calls", detail: "Each tool call with its input and result", events: ["tool.completed"], patterns: ["tool.completed"]},
];

// Whether pattern (an event type, a group such as task.*, or *) selects
// event, as the server decides it.
export function webhookEventMatches(pattern: string, event: string): boolean {
  if (pattern === "*" || pattern === event) return true;
  return pattern.endsWith(".*") && event.startsWith(pattern.slice(0, -1));
}

// The groups the patterns select whole.
export function selectedGroups(patterns: string[]): Set<string> {
  return new Set(
    WEBHOOK_EVENT_GROUPS.filter((group) => group.events.every((event) => patterns.some((pattern) => webhookEventMatches(pattern, event)))).map(
      (group) => group.id,
    ),
  );
}

// The patterns that select events outside the groups they select whole,
// such as task.completed alone: kept as they are when the groups change.
export function otherPatterns(patterns: string[]): string[] {
  const whole = selectedGroups(patterns);
  const covered = new Set(WEBHOOK_EVENT_GROUPS.filter((group) => whole.has(group.id)).flatMap((group) => group.events));
  const events = WEBHOOK_EVENT_GROUPS.flatMap((group) => group.events);
  return patterns.filter((pattern) => events.some((event) => webhookEventMatches(pattern, event) && !covered.has(event)));
}

// The patterns for the chosen groups, plus others to keep: * for all of
// them.
export function patternsFor(groups: Set<string>, others: string[] = []): string[] {
  if (WEBHOOK_EVENT_GROUPS.every((group) => groups.has(group.id))) return ["*"];
  const patterns = WEBHOOK_EVENT_GROUPS.filter((group) => groups.has(group.id)).flatMap((group) => group.patterns);
  return [...patterns, ...others.filter((pattern) => !patterns.includes(pattern))];
}
