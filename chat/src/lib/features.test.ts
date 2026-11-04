import {describe, expect, test} from "bun:test";
import {features} from "./features";

const all = {steer: true, interrupt: true, resume: true, images: true, settings: true, compact: true};

describe("features", () => {
  test("an agent's features follow its capabilities and the shell", () => {
    const full = features(all, true);
    expect(full.shell && full.settings && full.steer && full.interrupt).toBe(true);
    const bare = features({steer: false, interrupt: false, resume: false, images: false}, false);
    expect(bare.shell || bare.settings || bare.steer || bare.interrupt).toBe(false);
    // While the agent starts there are no capabilities yet.
    expect(features(null, true).steer).toBe(false);
  });

  test("session history for the agents with session logs", () => {
    expect(features(null, false, "claude").sessionHistory).toBe(true);
    expect(features(null, false, "pi").sessionHistory).toBe(true);
    expect(features(null, false, "custom").sessionHistory).toBe(false);
    expect(features(null, false, "claude").subagents).toBe(true);
    expect(features(null, false, "pi").subagents).toBe(false);
    expect(features(null, false, "codex").cost).toBe(true);
    expect(features(null, false, "custom").cost).toBe(false);
  });
});
