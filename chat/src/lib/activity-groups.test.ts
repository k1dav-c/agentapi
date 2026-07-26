import {describe, expect, test} from "bun:test";
import {groupConsecutiveTools} from "./activity-groups";

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
