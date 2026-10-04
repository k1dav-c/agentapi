import {describe, expect, test} from "bun:test";
import {describeTerminalPrompt, isMultiSelect, parseTerminalOptions, terminalOptionKeystrokes} from "./terminal-option";
import {readFileSync} from "node:fs";

describe("terminalOptionKeystrokes", () => {
  test("Claude selects numbered options without Enter", () => {
    expect(terminalOptionKeystrokes("claude", "3")).toBe("3");
  });

  test("Codex selects numbered options without Enter", () => {
    expect(terminalOptionKeystrokes("codex", "2")).toBe("2");
  });

  test("other agents confirm numbered options with Enter", () => {
    expect(terminalOptionKeystrokes("aider", "2")).toBe("2\r");
  });

  test("Enter-only and arrow options are not doubled", () => {
    expect(terminalOptionKeystrokes("claude", "\r")).toBe("\r");
    expect(terminalOptionKeystrokes("codex", "\r")).toBe("\r");
    expect(terminalOptionKeystrokes("codex", "\x1b[B\r")).toBe("\x1b[B\r");
  });
});

describe("parseTerminalOptions", () => {
  test("ignores a numbered list in the answer above the dialog", () => {
    const prompt = [
      "● Plan:",
      "  1. Refactor the parser",
      "  2. Add tests",
      "  3. Update docs",
      "",
      " Do you want to create c.txt?",
      " ❯ 1. Yes",
      "   2. Yes, and switch to accept edits (auto-approve file edits and common file",
      "      commands) for this session (shift+tab)",
      "   3. No",
      " Esc to cancel · Tab to amend",
    ].join("\n");
    expect(parseTerminalOptions(prompt)).toEqual([
      {key: "1", label: "Yes"},
      {key: "2", label: "Yes, and switch to accept edits (auto-approve file edits and common file"},
      {key: "3", label: "No"},
    ]);
  });

  test("Codex options", () => {
    const prompt = [
      "  Update available · 0.158.0 → 0.160.0",
      "› 1. Update now",
      "  2. Skip",
      "  3. Skip until next version",
      "",
      "  enter continue · esc skip",
    ].join("\n");
    expect(parseTerminalOptions(prompt).map((o) => o.label)).toEqual(["Update now", "Skip", "Skip until next version"]);
  });

  test("unnumbered options move the cursor relative to the highlighted one", () => {
    const prompt = [
      " Claude Code'll be able to read, edit, and execute files here.",
      "",
      " ❯ No, exit",
      "   Yes, I trust this folder",
      "",
      " Enter to confirm · Esc to cancel",
    ].join("\n");
    expect(parseTerminalOptions(prompt)).toEqual([
      {key: "\r", label: "No, exit"},
      {key: "\x1b[B\r", label: "Yes, I trust this folder"},
    ]);
  });

  test("plain text has no options", () => {
    expect(parseTerminalOptions("just text")).toEqual([]);
  });
});

describe("describeTerminalPrompt", () => {
  const fixture = (name: string) => readFileSync(new URL(`../../../lib/httpapi/testdata/${name}`, import.meta.url), "utf8");

  test("Claude permission dialog: question and the command to approve", () => {
    const {title, context} = describeTerminalPrompt(fixture("claude_permission_dialog.txt"));
    expect(title).toBe("Do you want to create c.txt?");
    expect(context).toContain("Create file");
  });

  test("Codex folder trust: the question ends mid-line", () => {
    const {title, context} = describeTerminalPrompt(fixture("codex_trust_prompt.txt"));
    expect(title).toBe("Trust this folder?");
    expect(context.join(" ")).toContain("Codex can read, edit, and run files here");
  });

  test("Codex update dialog has no question: the first line is the title", () => {
    const {title} = describeTerminalPrompt(fixture("codex_update_prompt.txt"));
    expect(title).toStartWith("Update available");
  });

  test("Claude folder trust", () => {
    const {title} = describeTerminalPrompt(fixture("claude_trust_dialog.txt"));
    expect(title).toBe("Quick safety check: Is this a project you created or one you trust?");
  });

  test("Claude multi-select: the tab bar is not context", () => {
    expect(describeTerminalPrompt(fixture("claude_multiselect_dialog.txt"))).toEqual({title: "Which fruits do you like?", context: []});
  });
});

describe("multi-select questions", () => {
  const prompt = readFileSync(new URL("../../../lib/httpapi/testdata/claude_multiselect_dialog.txt", import.meta.url), "utf8");
  test("checkbox options carry their state without the box", () => {
    const options = parseTerminalOptions(prompt);
    expect(isMultiSelect(options)).toBe(true);
    expect(options.map((option) => [option.key, option.label, option.checked])).toEqual([
      ["1", "Apple", true],
      ["2", "Banana", false],
      ["3", "Cherry", true],
      ["4", "Durian", false],
      ["5", "Type something", false],
      ["6", "Chat about this", undefined],
    ]);
  });
  test("single-select options are not multi-select", () => {
    expect(isMultiSelect(parseTerminalOptions("Proceed?\n❯ 1. Yes\n  2. No"))).toBe(false);
  });
});

describe("Pi dialogs", () => {
  const prompt = readFileSync(new URL("../../../lib/httpapi/testdata/pi_trust_dialog.txt", import.meta.url), "utf8");
  test("project trust: the arrow marks the highlighted option", () => {
    const options = parseTerminalOptions(prompt);
    expect(options.map((option) => option.label)).toEqual([
      "Trust",
      "Trust parent folder (/tmp/claude-1000)",
      "Trust (this session only)",
      "Do not trust",
      "Do not trust (this session only)",
    ]);
    expect(options[0].key).toBe("\r");
    expect(options[3].key).toBe("\x1b[B\x1b[B\x1b[B\r");
  });
  test("project trust: question and folder", () => {
    const {title, context} = describeTerminalPrompt(prompt);
    expect(title).toBe("Trust project folder?");
    expect(context[0]).toBe("/tmp/claude-1000/pi-trust-proj");
  });
});
