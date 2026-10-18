import {describe, expect, test} from "bun:test";
import {agentErrorKey} from "./agent-errors";

describe("agent error toasts", () => {
  test("a warning shows once, an error each time it happens", () => {
    const warning = "Codex is ignoring 1 unrecognized configuration setting.";
    expect(agentErrorKey({message: warning, level: "warning", time: "2026-10-18T10:00:00Z"})).toBe(
      agentErrorKey({message: warning, level: "warning", time: "2026-10-18T10:05:00Z"}),
    );
    expect(agentErrorKey({message: "Turn failed", level: "error", time: "2026-10-18T10:00:00Z"})).not.toBe(
      agentErrorKey({message: "Turn failed", level: "error", time: "2026-10-18T10:05:00Z"}),
    );
  });
});
