import {describe, expect, test} from "bun:test";
import {features} from "./features";

const all = {steer: true, interrupt: true, resume: true, images: true, settings: true, compact: true};

describe("features", () => {
  test("a terminal agent has its terminal, and nothing native", () => {
    const pty = features("pty", null, false);
    expect(pty.agentTerminal).toBe(true);
    expect(Object.entries(pty).filter(([, on]) => on).map(([name]) => name)).toEqual(["agentTerminal"]);
  });
  test("ACP has neither", () => {
    expect(Object.values(features("acp", all, true)).some(Boolean)).toBe(false);
  });
  test("a native agent's features follow its capabilities and the shell", () => {
    const full = features("native", all, true);
    expect(full.agentTerminal).toBe(false);
    expect(full.shell && full.requests && full.settings && full.steer && full.interrupt).toBe(true);
    const bare = features("native", {steer: false, interrupt: false, resume: false, images: false}, false);
    expect(bare.shell || bare.settings || bare.steer || bare.interrupt).toBe(false);
    expect(bare.requests && bare.slashCommands && bare.usage).toBe(true);
    // While the agent starts there are no capabilities yet.
    expect(features("native", null, true).steer).toBe(false);
  });

  test("session history for the agents with session logs", () => {
    expect(features("native", null, false, "claude").sessionHistory).toBe(true);
    expect(features("native", null, false, "pi").sessionHistory).toBe(true);
    expect(features("native", null, false, "custom").sessionHistory).toBe(false);
    expect(features("pty", null, false, "claude").sessionHistory).toBe(false);
  });
});
