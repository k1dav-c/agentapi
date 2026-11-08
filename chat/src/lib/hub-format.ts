// What a row says of an instance: what it does or waits for, and since
// when.

import {instanceState, type HubInstance, type Summary} from "./hub";

const requestLabels: Record<string, string> = {approval: "Approval", question: "Question", plan: "Plan"};

// Activity is the line under a row's name: what the instance does or waits
// for, and the tone to show it in.
export interface Activity {
  // label names what it is, when that adds to the text: "Approval",
  // "Working", "Finished".
  label?: string;
  text: string;
  tone: "needs" | "working" | "ready" | "fault" | "muted";
  // since is when it began: the request, the task or its end.
  since?: string;
}

// waitsFor is what an instance that needs you waits for.
function waitsFor(summary: Summary | null | undefined): Activity {
  if (summary?.attention === "login") return {label: "Login", text: summary.login_required?.message || "Log in to continue", tone: "needs"};
  if (summary?.attention === "limit") return {label: "Usage limit", text: summary.limit_stop?.message || "The account reached its limit", tone: "needs", since: summary.limit_stop?.stopped_at};
  const request = summary?.request;
  if (request) return {label: requestLabels[request.kind] ?? "Request", text: request.title, tone: "needs", since: request.created_at};
  return {label: "Needs you", text: "Waiting for your answer", tone: "needs"};
}

export function activity(instance: HubInstance): Activity {
  const summary = instance.summary;
  const task = summary?.task;
  switch (instanceState(instance)) {
    case "needs-you":
      return waitsFor(summary);
    case "muted":
      // What it waits for, out of the way.
      return {...waitsFor(summary), label: "Muted", tone: "muted"};
    case "working":
      if (summary?.retry_wait) {
        // A failed turn AgentAPI tries again by itself.
        const wait = summary.retry_wait;
        const failed = wait.api_status ? `The API returned ${wait.api_status}` : "The API request failed";
        return {label: "Retrying", text: `${failed} · try ${wait.attempt} of ${wait.attempts}`, tone: "working", since: wait.stopped_at};
      }
      return {label: "Working", text: task?.prompt || summary?.title || "On a task", tone: "working", since: task?.started_at};
    case "unread":
      return {label: "Finished", text: summary?.title || task?.prompt || "A task", tone: "ready", since: task?.ended_at};
    case "unreachable":
      return {label: "Unreachable", text: shortError(instance.error) || "The hub can't reach it", tone: "fault", since: instance.last_seen};
    case "stopped":
      return {label: summary?.attention === "exited" ? "Exited" : "Stopped", text: summary?.title || "Not running", tone: "muted", since: instance.last_seen};
    case "connecting":
      return {text: "Connecting…", tone: "muted"};
    case "idle":
      return {text: summary?.title || "No task yet", tone: "muted", since: task?.ended_at};
    case "unknown":
      return {text: "Reload the page to see what it does", tone: "muted"};
  }
}

// shortError keeps what a person needs of a connection error: Go's chain
// "unreachable: Get "…": dial tcp …: connect: connection refused" ends in
// the part that says what happened.
export function shortError(error: string | undefined): string {
  if (!error) return "";
  if (!error.startsWith("unreachable:")) return error;
  const last = error.slice(error.lastIndexOf(": ") + 2).trim();
  return last ? last[0].toUpperCase() + last.slice(1) : error;
}

// formatAgo is a short age: "now", "45s", "12m", "3h", "2d".
export function formatAgo(since: string | undefined, now: number): string {
  if (!since) return "";
  const seconds = Math.max(0, Math.round((now - Date.parse(since)) / 1000));
  if (Number.isNaN(seconds)) return "";
  if (seconds < 10) return "now";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

// formatElapsed is a running duration: "45s", "2m 14s", "1h 05m".
export function formatElapsed(since: string | undefined, now: number): string {
  if (!since) return "";
  const seconds = Math.max(0, Math.floor((now - Date.parse(since)) / 1000));
  if (Number.isNaN(seconds)) return "";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
  return `${Math.floor(seconds / 3600)}h ${String(Math.floor((seconds % 3600) / 60)).padStart(2, "0")}m`;
}
