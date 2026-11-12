import { describe, expect, test } from "bun:test";
import { effortsOf, modeMeta, modelChange, modelGroups, modelLabel, modelValue, providerLabel } from "./session-settings";
import type { SettingOptions } from "./chat-api";

const options: SettingOptions = {
  permission_modes: ["bypass", "ask"],
  models: [
    { id: "default", name: "Default (recommended)", efforts: ["low", "high"] },
    { id: "haiku", name: "Haiku" },
  ],
  providers: [
    { id: "kimi", name: "Kimi", host: "gw.example.com", origin: "config", models: [{ id: "Kimi-K3", name: "Kimi K3" }] },
    { id: "empty", name: "Empty", host: "gw.example.com", origin: "hub", models: [] },
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

  test("providers", () => {
    expect(modelLabel(options, "Kimi-K3", "kimi")).toBe("Kimi K3");
    expect(modelLabel(options, "Kimi-K3", "default")).toBe("Kimi-K3");
    expect(providerLabel(options, "kimi")).toBe("Kimi");
    expect(providerLabel(options, "default")).toBe("");
    expect(providerLabel(options, undefined)).toBe("");
    // A provider without models can't be picked from the menu.
    expect(modelGroups(options).map((g) => g.provider)).toEqual(["default", "kimi"]);
    expect(modelGroups(null)).toEqual([]);
  });

  test("model changes", () => {
    // Another model of the same provider changes only the model.
    expect(modelChange(modelValue("default", "haiku"), undefined)).toEqual({ model: "haiku" });
    expect(modelChange(modelValue("kimi", "Kimi-K3"), "kimi")).toEqual({ model: "Kimi-K3" });
    // Another provider's switches it too.
    expect(modelChange(modelValue("kimi", "Kimi-K3"), "default")).toEqual({ provider: "kimi", model: "Kimi-K3" });
    expect(modelChange(modelValue(undefined, "default"), "kimi")).toEqual({ provider: "default", model: "default" });
  });
});
