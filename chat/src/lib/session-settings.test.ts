import { describe, expect, test } from "bun:test";
import { effortsOf, modeMeta, modelLabel } from "./session-settings";
import type { SettingOptions } from "./chat-api";

const options: SettingOptions = {
  permission_modes: ["bypass", "ask"],
  models: [
    { id: "default", name: "Default (recommended)", efforts: ["low", "high"] },
    { id: "haiku", name: "Haiku" },
  ],
};

describe("session settings", () => {
  test("modes", () => {
    expect(modeMeta("bypass").label).toBe("Bypass");
    expect(modeMeta(undefined).label).toBe("Custom");
    // A mode a newer server added shows by its name.
    expect(modeMeta("dontAsk" as Parameters<typeof modeMeta>[0]).label).toBe("dontAsk");
  });

  test("model labels", () => {
    expect(modelLabel(options, "default")).toBe("Default (recommended)");
    expect(modelLabel(options, "claude-opus-4-1")).toBe("claude-opus-4-1");
    expect(modelLabel(null, "default")).toBe("default");
  });

  test("efforts", () => {
    expect(effortsOf(options, "default")).toEqual(["low", "high"]);
    expect(effortsOf(options, "haiku")).toEqual([]);
    expect(effortsOf(options, "custom")).toEqual([]);
    expect(effortsOf(null, "default")).toEqual([]);
    expect(effortsOf(options, undefined)).toEqual([]);
  });
});
