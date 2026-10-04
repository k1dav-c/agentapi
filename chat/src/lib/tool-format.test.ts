import {describe, expect, test} from "bun:test";
import {getExitCode, getToolCommand, getToolSummary} from "./tool-format";

describe("getToolCommand", () => {
  test("Claude Bash", () => {
    expect(getToolCommand("Bash", {command: "go test ./...", description: "Run tests"})).toBe("go test ./...");
  });
  test("Codex exec_command with a string or argv", () => {
    expect(getToolCommand("exec_command", {cmd: "rg --files"})).toBe("rg --files");
    expect(getToolCommand("shell", {command: ["bash", "-lc", "pwd"]})).toBe("pwd");
  });
  test("Codex freeform exec", () => {
    expect(getToolCommand("exec", "ls -la\n")).toBe("ls -la");
  });
  test("not a command tool", () => {
    expect(getToolCommand("Read", {file_path: "/a/b.go"})).toBeUndefined();
    expect(getToolCommand("apply_patch", "*** Begin Patch")).toBeUndefined();
  });
  test("summary falls back to the command for Codex", () => {
    expect(getToolSummary("exec", "rg --files lib/httpapi -g '*_test.go'")).toBe("rg --files lib/httpapi -g '*_test.go'");
  });
});

describe("getExitCode", () => {
  test("reads common formats", () => {
    expect(getExitCode("Exit code: 1\nWall time: 0.2s")).toBe(1);
    expect(getExitCode("Process exited with code 0")).toBe(0);
    expect(getExitCode("ok  github.com/x 0.6s")).toBeUndefined();
  });
});
