import {describe, expect, test} from "bun:test";
import {agentErrorKey, dismissedErrors, MAX_DISMISSED_SERVERS, rememberDismissed} from "./agent-errors";

// An in-memory localStorage.
function memoryStorage() {
  const items = new Map<string, string>();
  return {
    items,
    getItem: (key: string) => items.get(key) ?? null,
    setItem: (key: string, value: string) => void items.set(key, value),
    removeItem: (key: string) => void items.delete(key),
    key: (index: number) => [...items.keys()][index] ?? null,
    get length() {
      return items.size;
    },
  };
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

  test("they are remembered for the servers closed in last, the others forgotten", () => {
    const storage = memoryStorage();
    rememberDismissed("http://first", "k", storage);
    for (let i = 0; i < MAX_DISMISSED_SERVERS; i++) rememberDismissed(`http://job-${i}`, "k", storage);
    expect(dismissedErrors("http://first", storage).size).toBe(0);
    expect(dismissedErrors("http://job-0", storage).size).toBe(1);
    // Closing one again keeps its server among the latest.
    rememberDismissed("http://job-0", "k2", storage);
    rememberDismissed("http://another", "k", storage);
    expect(dismissedErrors("http://job-0", storage).size).toBe(2);
    expect(dismissedErrors("http://job-1", storage).size).toBe(0);
    expect([...storage.items.keys()].filter((key) => key.startsWith("agentapi.chat.dismissed-errors:")).length).toBe(MAX_DISMISSED_SERVERS);
  });

  test("the servers a browser kept them for before the index count too", () => {
    const storage = memoryStorage();
    for (let i = 0; i < MAX_DISMISSED_SERVERS + 5; i++) storage.setItem(`agentapi.chat.dismissed-errors:http://old-${i}`, '["k"]');
    rememberDismissed("http://new", "k", storage);
    expect(dismissedErrors("http://new", storage).size).toBe(1);
    expect(dismissedErrors("http://old-0", storage).size).toBe(0);
    expect(dismissedErrors(`http://old-${MAX_DISMISSED_SERVERS + 4}`, storage).size).toBe(1);
    expect([...storage.items.keys()].filter((key) => key.startsWith("agentapi.chat.dismissed-errors:")).length).toBe(MAX_DISMISSED_SERVERS);
  });
});
