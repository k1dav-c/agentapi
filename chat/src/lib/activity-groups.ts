export type GroupableActivity<TMessage, TTool> =
  | {type: "message"; key: string; message: TMessage}
  | {type: "prompt"; key: string; message: TMessage}
  | {type: "tool"; key: string; toolCall: TTool}
  | {type: "thinking"; key: string; content: string; timestamp: string};

export type GroupedActivity<TMessage, TTool> =
  | GroupableActivity<TMessage, TTool>
  | {type: "tool-group"; key: string; toolCalls: TTool[]};

export function groupConsecutiveTools<TMessage, TTool extends {id: string}>(
  activity: GroupableActivity<TMessage, TTool>[],
): GroupedActivity<TMessage, TTool>[] {
  const grouped: GroupedActivity<TMessage, TTool>[] = [];
  for (let index = 0; index < activity.length; index += 1) {
    const item = activity[index];
    if (item.type !== "tool") {
      grouped.push(item);
      continue;
    }

    const tools = [item.toolCall];
    while (activity[index + 1]?.type === "tool") {
      index += 1;
      tools.push(
        (activity[index] as Extract<
          GroupableActivity<TMessage, TTool>,
          {type: "tool"}
        >).toolCall,
      );
    }
    grouped.push(
      tools.length === 1
        ? item
        : {
            type: "tool-group",
            key: `tool-group-${tools[0].id}-${tools.at(-1)!.id}`,
            toolCalls: tools,
          },
    );
  }
  return grouped;
}

// splitAtThinking splits a task's steps where thinking starts, so that each
// part holds thinking with the steps it explains. On a wide screen a part
// shows its thinking in the right margin beside its steps and is as tall as
// the taller of the two, so a long note makes room beside its steps instead
// of pushing the notes after it below the steps they belong to. Each item
// keeps its index in the steps.
export function splitAtThinking<T extends {type: string}>(steps: T[]): {item: T; index: number}[][] {
  const parts: {item: T; index: number}[][] = [];
  steps.forEach((item, index) => {
    const startsPart = item.type === "thinking" && steps[index - 1]?.type !== "thinking";
    if (startsPart || parts.length === 0) parts.push([]);
    parts[parts.length - 1].push({item, index});
  });
  return parts;
}
