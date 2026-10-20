import {describe, expect, test} from "bun:test";
import {agentErrorKey, dismissedErrors, rememberDismissed} from "./agent-errors";

// An in-memory localStorage.
function memoryStorage() {
  const items = new Map<string, string>();
  return {getItem: (key: string) => items.get(key) ?? null, setItem: (key: string, value: string) => void items.set(key, value)};
}

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

  test("closed notifications are remembered per server", () => {
    const storage = memoryStorage();
    expect(dismissedErrors("http://a", storage).size).toBe(0);
    rememberDismissed("http://a", "k1", storage);
    rememberDismissed("http://a", "k2", storage);
    rememberDismissed("http://a", "k1", storage);
    expect([...dismissedErrors("http://a", storage)]).toEqual(["k2", "k1"]);
    expect(dismissedErrors("http://b", storage).size).toBe(0);
    // The latest 200 only.
    for (let i = 0; i < 250; i++) rememberDismissed("http://a", `n${i}`, storage);
    const kept = dismissedErrors("http://a", storage);
    expect(kept.size).toBe(200);
    expect(kept.has("n249")).toBe(true);
    expect(kept.has("n0")).toBe(false);
  });

  test("broken or missing storage remembers nothing", () => {
    const storage = memoryStorage();
    storage.setItem("agentapi.chat.dismissed-errors:http://a", "{not json");
    expect(dismissedErrors("http://a", storage).size).toBe(0);
    expect(dismissedErrors("http://a", null).size).toBe(0);
    rememberDismissed("http://a", "k", null);
  });
});
