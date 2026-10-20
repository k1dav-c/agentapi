// The subagents the agent ran (Claude Code's Agent tool), for the Lineage
// panel: which tool calls started one, and a subagent's run as a task the
// transcript can show.

import type {RichMessage} from "./conversation";
import type {SubagentRun} from "./chat-api";
import {buildTimeline, collectToolCalls, type TaskSection} from "./task-timeline";

// The tools that start a subagent: Claude Code's Agent (Task before 2.1),
// Codex's spawn_agent.
const SUBAGENT_TOOLS = new Set(["Agent", "Task", "spawn_agent"]);

export function isSubagentTool(name: string | undefined): boolean {
  return name !== undefined && SUBAGENT_TOOLS.has(name);
}

// How many subagents the agent started in its rich messages, and how many
// of them still run: their tool call has no result yet, or one that says
// the subagent runs in the background.
export function subagentCounts(richMessages: RichMessage[]): {total: number; running: number} {
  const started = new Set<string>();
  const results = new Map<string, string | undefined>();
  for (const message of richMessages) {
    for (const block of message.content) {
      if (block.type === "tool_use" && block.tool_use_id && isSubagentTool(block.tool_name)) started.add(block.tool_use_id);
      if (block.type === "tool_result" && block.tool_use_id) results.set(block.tool_use_id, block.status);
    }
  }
  let running = 0;
  for (const id of started) {
    if (!results.has(id) || results.get(id) === "running") running++;
  }
  return {total: started.size, running};
}

// subagentTask turns a subagent's run into a task: its prompt, then its
// activity.
export function subagentTask(run: SubagentRun): TaskSection {
  const time = run.messages[0]?.timestamp ?? run.subagent.started_at;
  const prompt = {id: 0, role: "user", content: run.prompt || run.subagent.description || "Subagent task", time: run.subagent.started_at < time ? run.subagent.started_at : time};
  const messages = run.messages as RichMessage[];
  const timeline = buildTimeline([prompt], messages, collectToolCalls(messages));
  return {...timeline.tasks[0], key: `subagent-${run.subagent.id}`};
}

// The event that opens the Lineage panel, on the subagent a tool call
// started, if given.
export const OPEN_LINEAGE_EVENT = "agentapi:open-lineage";

export function openLineage(toolUseId?: string) {
  window.dispatchEvent(new CustomEvent<string | undefined>(OPEN_LINEAGE_EVENT, {detail: toolUseId}));
}
