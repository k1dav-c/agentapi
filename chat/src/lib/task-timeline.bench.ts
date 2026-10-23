// What the chat works out on every streamed update of a long session,
// before React renders: a synthetic session of 50, 100 and 200 tasks (20
// tool calls each with long results; 18.6M characters at 200), whose last
// reply grows one update at a time. Run it from chat/ with
//
//   bun src/lib/task-timeline.bench.ts

import {richMessagesSince, type Message, type RichMessage} from "./conversation";
import {computeTokenTotals, taskProgress, TokenCounter} from "./session-status";
import {subagentCounts} from "./subagents";
import {filterTasks} from "./task-filter";
import {TimelineBuilder} from "./task-timeline";

const STEPS = 20;

function syntheticSession(taskCount: number) {
  const messages: Message[] = [];
  const richMessages: RichMessage[] = [];
  const start = Date.parse("2026-10-01T00:00:00Z");
  let id = 0;
  let characters = 0;
  for (let t = 0; t < taskCount; t++) {
    const base = start + t * 600_000;
    const at = (s: number) => new Date(base + s * 1000).toISOString();
    const prompt = `Task ${t}: fix the failing parser tests and explain what was wrong. `.repeat(3);
    messages.push({id: id++, role: "user", content: prompt, time: at(0)});
    richMessages.push({message_id: `u-${t}`, role: "user", timestamp: at(0), content: [{type: "text", text: prompt}]});
    characters += prompt.length * 2;
    for (let s = 0; s < STEPS; s++) {
      const thinking = `**Step ${s} of task ${t}**\n\nI'll run the tests of package ${s} to see what fails. `.repeat(3);
      const result = `--- FAIL: TestParse${t}_${s} (0.00s)\n    parser_test.go:${s}: expected token, got EOF\n`.repeat(53);
      characters += thinking.length + result.length;
      richMessages.push({
        message_id: `a-${t}-${s}`,
        role: "assistant",
        timestamp: at(1 + s * 2),
        usage: {input_tokens: 10, output_tokens: 50, cache_creation_input_tokens: 0, cache_read_input_tokens: 1000},
        content: [
          {type: "thinking", thinking},
          {type: "tool_use", tool_use_id: `t-${t}-${s}`, tool_name: s === 3 ? "Agent" : "Bash", tool_input: {command: `go test ./pkg${s}/... -run TestParse${t}`}, status: "completed"},
        ],
      });
      richMessages.push({message_id: `r-${t}-${s}`, role: "user", timestamp: at(2 + s * 2), content: [{type: "tool_result", tool_use_id: `t-${t}-${s}`, text: result}]});
    }
    const reply = `The parser dropped the last token of task ${t}. Fixed it and the tests pass. `.repeat(12);
    richMessages.push({message_id: `f-${t}`, role: "assistant", timestamp: at(60), content: [{type: "text", text: reply}]});
    const screen = `● ${reply}\n`.repeat(3);
    messages.push({id: id++, role: "agent", content: screen, time: at(61)});
    characters += reply.length + screen.length;
  }
  return {messages, richMessages, characters};
}

// One streamed update: the last reply grows, in the transcript and on the
// screen, as the chat merges it (a copy of the list, one item replaced).
function streamUpdate<T extends {messages: Message[]; richMessages: RichMessage[]}>(session: T, n: number): T {
  const richMessages = session.richMessages.slice();
  const last = richMessages[richMessages.length - 1];
  richMessages[richMessages.length - 1] = {...last, content: [{type: "text", text: `${last.content[0].text} more ${n}`}]};
  const messages = session.messages.slice();
  const lastMessage = messages[messages.length - 1];
  messages[messages.length - 1] = {...lastMessage, content: `${lastMessage.content} more ${n}`};
  return {...session, messages, richMessages};
}

function bench(taskCount: number) {
  let session = syntheticSession(taskCount);
  // What the chat works out on an update (useTranscript, MessageList): the
  // transcript, the tokens, the current task's progress and the subagents.
  const builder = new TimelineBuilder();
  const counter = new TokenCounter();
  const update = () => {
    const {timeline, toolCalls} = builder.build(session.messages, session.richMessages);
    filterTasks(timeline.tasks, "running", "all", "");
    counter.count(session.richMessages);
    const task = timeline.tasks.findLast((candidate) => candidate.prompt.id !== undefined);
    computeTokenTotals(richMessagesSince(session.richMessages, task?.prompt.time));
    taskProgress(task);
    subagentCounts(toolCalls);
  };
  update();
  const runs = 40;
  const warmup = 10;
  let total = 0;
  for (let i = 0; i < runs; i++) {
    session = streamUpdate(session, i);
    const started = performance.now();
    update();
    if (i >= warmup) total += performance.now() - started;
  }
  const ms = total / (runs - warmup);
  // A full replay, as after a reconnect to a new epoch: every message again,
  // as a new object.
  const replayed = {...session, messages: structuredClone(session.messages), richMessages: structuredClone(session.richMessages)};
  const started = performance.now();
  builder.build(replayed.messages, replayed.richMessages);
  const replay = performance.now() - started;
  console.log(`${taskCount} tasks, ${(session.characters / 1e6).toFixed(1)}M characters: ${ms.toFixed(2)} ms per update, ${replay.toFixed(0)} ms for a full replay`);
}

if (import.meta.main) {
  for (const count of [50, 100, 200]) bench(count);
}
