import {describe, expect, test} from "bun:test";
import {groupConsecutiveTools, splitAtThinking} from "./activity-groups";

describe("groupConsecutiveTools", () => {
  test("groups only adjacent tool calls", () => {
    const activity = [
      {type: "tool" as const, key: "a", toolCall: {id: "a"}},
      {type: "tool" as const, key: "b", toolCall: {id: "b"}},
      {type: "message" as const, key: "m", message: "done"},
      {type: "tool" as const, key: "c", toolCall: {id: "c"}},
    ];
    const grouped = groupConsecutiveTools(activity);
    expect(grouped).toHaveLength(3);
    expect(grouped[0]).toEqual({
      type: "tool-group",
      key: "tool-group-a-b",
      toolCalls: [{id: "a"}, {id: "b"}],
    });
    expect(grouped[1].type).toBe("message");
    expect(grouped[2].type).toBe("tool");
  });
});

describe("thinking with the steps it explains", () => {
  test("a part starts where thinking starts", () => {
    const steps = ["message", "thinking", "thinking", "tool", "message", "thinking", "tool"].map((type, i) => ({type, key: `${i}`}));
    expect(splitAtThinking(steps).map((part) => part.map(({item, index}) => `${item.type}@${index}`))).toEqual([
      ["message@0"],
      ["thinking@1", "thinking@2", "tool@3", "message@4"],
      ["thinking@5", "tool@6"],
    ]);
    expect(splitAtThinking([])).toEqual([]);
  });
});
