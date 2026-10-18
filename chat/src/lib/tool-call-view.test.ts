import {describe, expect, test} from "bun:test";
import {describeToolCall} from "./tool-call-view";

describe("describeToolCall", () => {
  test("a command", () => {
    const view = describeToolCall({
      id: "t1",
      name: "Bash",
      input: {command: "go test ./..."},
      result: "ok  a\nok  b\nok  c\nok  d\nok  e\n\n",
      status: "completed",
      timestamp: "2026-01-01T00:00:00Z",
      resultTimestamp: "2026-01-01T00:00:02.500Z",
    });
    expect(view.command).toBe("go test ./...");
    expect(view.pending).toBe(false);
    expect(view.failed).toBe(false);
    expect(view.durationMs).toBe(2500);
    expect(view.resultPreview).toBe("ok  a\nok  b\nok  c\nok  d");
    expect(view.targets).toEqual([]);
  });

  test("a running tool without a result is pending; an error fails it", () => {
    expect(describeToolCall({id: "t2", name: "Read", input: {file_path: "/a.go"}, timestamp: "2026-01-01T00:00:00Z"}).pending).toBe(true);
    const failed = describeToolCall({id: "t3", name: "Read", input: {}, result: "no such file", isError: true, timestamp: "2026-01-01T00:00:00Z"});
    expect(failed.failed).toBe(true);
    expect(failed.pending).toBe(false);
    // Only a command previews its output.
    expect(failed.resultPreview).toBe("");
  });

  test("a file tool lists its file", () => {
    const view = describeToolCall({id: "t4", name: "Read", input: {file_path: "/src/main.go"}, timestamp: "2026-01-01T00:00:00Z"});
    expect(view.targets.map((target) => target.path)).toEqual(["/src/main.go"]);
    expect(view.label).toBe("Read");
    expect(view.command).toBeUndefined();
  });
});
