import {describe, expect, test} from "bun:test";
import {findTerminalLinks, shortLink, type TerminalLine} from "./terminal-links";

const lines = (...texts: (string | [string, boolean])[]): TerminalLine[] =>
  texts.map((text) => (typeof text === "string" ? {text, wrapped: false} : {text: text[0], wrapped: text[1]}));

describe("terminal links", () => {
  test("finds links on a line, without the punctuation after them", () => {
    expect(findTerminalLinks(lines("See https://example.com/a, and (https://example.com/b)."), 80)).toEqual([
      {url: "https://example.com/a", start: {line: 0, column: 4}, end: {line: 0, column: 24}},
      {url: "https://example.com/b", start: {line: 0, column: 32}, end: {line: 0, column: 52}},
    ]);
  });

  test("follows a link the terminal wrapped over several lines", () => {
    const url = "https://auth.openai.com/oauth/authorize?response_type=code&client_id=app_EMoamEEZ73f0CkXaXp7hrann";
    const cols = 40;
    const text = `Open ${url} to log in`;
    const rows = [];
    for (let i = 0; i < text.length; i += cols) rows.push({text: text.slice(i, i + cols), wrapped: i > 0});
    const [link] = findTerminalLinks(rows, cols);
    expect(link.url).toBe(url);
    expect(link.start).toEqual({line: 0, column: 5});
    expect(link.end).toEqual({line: 2, column: (5 + url.length - 1) % cols});
  });

  test("joins a link a program broke at the terminal's width", () => {
    const cols = 30;
    const first = "https://example.com/a/long/pa";
    expect(first.length).toBe(cols - 1);
    expect(findTerminalLinks(lines(first, "th?x=1", "Done"), cols).map((link) => link.url)).toEqual([
      "https://example.com/a/long/path?x=1",
    ]);
    // A short line ends the link; the next line is text of its own.
    expect(findTerminalLinks(lines("https://example.com/a", "next"), cols).map((link) => link.url)).toEqual(["https://example.com/a"]);
  });

  test("counts the cells of wide characters", () => {
    const text = "登入 https://example.com/x";
    const columns = [0, 2, 4, ...Array.from({length: text.length - 3}, (_, i) => 5 + i)];
    const [link] = findTerminalLinks([{text, wrapped: false, columns}], 80);
    expect(link.start).toEqual({line: 0, column: 5});
    expect(link.end).toEqual({line: 0, column: 5 + "https://example.com/x".length - 1});
  });

  test("leaves out links cut short and text that only looks like one", () => {
    expect(findTerminalLinks(lines("https://example.com/agent… and https://"), 80)).toEqual([]);
  });

  test("shows a link short", () => {
    expect(shortLink("https://auth.openai.com/codex/device")).toBe("auth.openai.com/codex/device");
    expect(shortLink("https://auth.openai.com/oauth/authorize?client_id=x")).toBe("auth.openai.com/oauth/authorize?…");
    expect(shortLink("https://example.com/" + "a".repeat(60), 20)).toBe("example.com/aaaaaaa…");
  });
});
