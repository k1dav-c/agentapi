// The subagents the agent ran (Claude Code's Agent tool), for the Lineage
// panel: which tool calls started one, and a subagent's run as a task the
// transcript can show.

import type {SubagentRun} from "./chat-api";
import {buildTranscript, type TaskSection, type ToolCall} from "./task-timeline";

// The tools that start a subagent: Claude Code's Agent (Task before 2.1),
// Codex's spawn_agent.
const SUBAGENT_TOOLS = new Set(["Agent", "Task", "spawn_agent"]);

export function isSubagentTool(name: string | undefined): boolean {
  return name !== undefined && SUBAGENT_TOOLS.has(name);
}

// How many subagents the agent started (its tool calls), and how many of
// them still run: their tool call has no result yet, or one that says the
// subagent runs in the background.
export function subagentCounts(toolCalls: ToolCall[]): {total: number; running: number} {
  let total = 0;
  let running = 0;
  for (const call of toolCalls) {
    if (!isSubagentTool(call.name)) continue;
    total++;
    if (call.resultTimestamp === undefined || call.status === "running") running++;
  }
  return {total, running};
}

// subagentTask turns a subagent's run into a task: its prompt, then its
// activity.
export function subagentTask(run: SubagentRun): TaskSection {
  const time = run.messages[0]?.timestamp ?? run.subagent.started_at;
  const prompt = {id: 0, role: "user", content: run.prompt || run.subagent.description || "Subagent task", time: run.subagent.started_at < time ? run.subagent.started_at : time};
  const {timeline} = buildTranscript([prompt], run.messages);
  return {...timeline.tasks[0], key: `subagent-${run.subagent.id}`};
}

// The event that opens the Lineage panel, on the subagent a tool call
// started, if given.
export const OPEN_LINEAGE_EVENT = "agentapi:open-lineage";

export function openLineage(toolUseId?: string) {
  window.dispatchEvent(new CustomEvent<string | undefined>(OPEN_LINEAGE_EVENT, {detail: toolUseId}));
}
