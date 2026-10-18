import {describe, expect, test} from "bun:test";
import {agentDisplayName, parseAgentType, updatesOnRestart} from "./agents";

describe("agent types", () => {
  test("a type this build doesn't know shows as custom", () => {
    expect(parseAgentType("claude")).toBe("claude");
    expect(parseAgentType("")).toBe("unknown");
    expect(parseAgentType(undefined)).toBe("unknown");
    expect(parseAgentType("brand-new-agent")).toBe("custom");
    expect(parseAgentType("toString")).toBe("custom");
  });

  test("names every type, with a fallback for unknown", () => {
    expect(agentDisplayName("codex", "x")).toBe("Codex");
    expect(agentDisplayName(parseAgentType("brand-new-agent"), "x")).toBe("Custom");
    expect(agentDisplayName("unknown", "The agent")).toBe("The agent");
  });

  test("a restart updates Codex and Pi first", () => {
    expect(updatesOnRestart("codex")).toBe(true);
    expect(updatesOnRestart("pi")).toBe(true);
    expect(updatesOnRestart("claude")).toBe(false);
    expect(updatesOnRestart("custom")).toBe(false);
    expect(updatesOnRestart("unknown")).toBe(false);
  });
});
