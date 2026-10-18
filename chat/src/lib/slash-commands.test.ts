import { describe, expect, test } from "bun:test";
import { SLASH_COMMANDS, loginCommand, matchingCommands, parseSlashCommand, pickChoice, runsHere } from "./slash-commands";

describe("slash commands", () => {
  test("parses the chat's commands", () => {
    expect(parseSlashCommand("/model sonnet")).toMatchObject({ info: { kind: "model" }, arg: "sonnet" });
    expect(parseSlashCommand("  /MODE  ask ")).toMatchObject({ info: { kind: "mode" }, arg: "ask" });
    expect(parseSlashCommand("/new")).toMatchObject({ info: { kind: "clear" }, arg: "" });
    expect(parseSlashCommand("/think high")).toMatchObject({ info: { kind: "effort" }, arg: "high" });
  });

  test("leaves the rest to the agent", () => {
    expect(parseSlashCommand("/code-review")).toBeNull();
    expect(parseSlashCommand("model sonnet")).toBeNull();
    expect(parseSlashCommand("/model\nand more")).toBeNull();
    expect(parseSlashCommand("/")).toBeNull();
  });

  test("completes the command word", () => {
    expect(matchingCommands("/mo").map((c) => c.name)).toEqual(["model", "mode"]);
    expect(matchingCommands("/sh").map((c) => c.name)).toEqual(["terminal"]);
    expect(matchingCommands("/model ")).toEqual([]);
    expect(matchingCommands("hello")).toEqual([]);
  });

  test("picks a choice by id or name", () => {
    const models = [
      { id: "default", name: "Default (recommended)" },
      { id: "sonnet", name: "Sonnet" },
      { id: "claude-fable-5-1[1m]", name: "Fable" },
    ];
    const keys = (m: { id: string; name: string }) => [m.id, m.name];
    expect(pickChoice("Sonnet", models, keys)?.id).toBe("sonnet");
    expect(pickChoice("fable", models, keys)?.id).toBe("claude-fable-5-1[1m]");
    expect(pickChoice("son", models, keys)?.id).toBe("sonnet");
    expect(pickChoice("x", models, keys)).toBeUndefined();
    expect(pickChoice("", models, keys)).toBeUndefined();
  });

  test("login commands", () => {
    expect(loginCommand("claude")).toBe("claude auth login");
    expect(loginCommand("codex")).toBe("codex login --device-auth");
    expect(loginCommand("aider")).toBeUndefined();
  });
});

describe("runsHere", () => {
  const named = (name: string) => SLASH_COMMANDS.find((command) => command.name === name)!;
  test("the terminal and login need the shell", () => {
    expect(runsHere(named("terminal"), {shell: false, settings: true})).toBe(false);
    expect(runsHere(named("login"), {shell: true, settings: false})).toBe(true);
  });
  test("the settings' commands need the settings", () => {
    for (const name of ["model", "effort", "mode", "plan"]) {
      expect(runsHere(named(name), {shell: true, settings: false})).toBe(false);
      expect(runsHere(named(name), {shell: false, settings: true})).toBe(true);
    }
  });
  test("the rest always run", () => {
    for (const name of ["compact", "clear", "help"]) {
      expect(runsHere(named(name), {shell: false, settings: false})).toBe(true);
    }
  });
});
