"use client";

import {useMemo, useState} from "react";
import {richMessagesSince, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";
import {computeTokenTotals, taskProgress, TokenCounter} from "@/lib/session-status";
import {subagentCounts} from "@/lib/subagents";
import {TimelineBuilder} from "@/lib/task-timeline";

// useTranscript works out from the messages what the transcript and the
// views around it show: the tasks and the tool calls, the tokens the
// session used, what the agent does in the task it works on (the last one
// sent) and the tokens that took, and the subagents it ran. Each update
// goes through what changed, or the current task, rather than the whole
// session, and a part that didn't change keeps its object: the components
// that select it don't render again.
export function useTranscript(messages: (Message | DraftMessage)[], richMessages: RichMessage[]) {
  const [builder] = useState(() => new TimelineBuilder());
  const [counter] = useState(() => new TokenCounter());
  const {timeline, toolCalls} = useMemo(() => builder.build(messages, richMessages), [builder, messages, richMessages]);
  const tokenTotals = useMemo(() => counter.count(richMessages), [counter, richMessages]);
  const task = timeline.tasks.findLast((candidate) => candidate.prompt.id !== undefined);
  const taskStart = task?.prompt.time;
  const taskTokens = useMemo(() => computeTokenTotals(richMessagesSince(richMessages, taskStart)), [richMessages, taskStart]);
  const {activity: taskActivity, latestTool} = useMemo(() => taskProgress(task), [task]);
  const subagents = useMemo(() => subagentCounts(toolCalls), [toolCalls]);
  return {timeline, toolCalls, tokenTotals, taskTokens, taskActivity, latestTool, subagents};
}
