import {describe, expect, test} from "bun:test";
import {OrderedInput, TTY_MAX_FONT_SIZE, TTY_MIN_FONT_SIZE, fitTerminalFontSize, screenToTerminalOutput} from "./tty";

describe("screenToTerminalOutput", () => {
  test("clears screen and scrollback, then writes lines with CRLF", () => {
    expect(screenToTerminalOutput("a\nb\r\nc")).toBe("\x1b[H\x1b[2J\x1b[3Ja\r\nb\r\nc");
  });

  test("collapses the blank padding of the tall emulated terminal", () => {
    const screen = ["", "  ", "● reply   ", "", "", "", "    ", "─────", "❯ ", "─────"].join("\n");
    expect(screenToTerminalOutput(screen)).toBe(
      "\x1b[H\x1b[2J\x1b[3J" + ["● reply", "", "─────", "❯", "─────"].join("\r\n"),
    );
  });
});

describe("OrderedInput", () => {
  test("sends one request at a time, in order, batching keys typed meanwhile", async () => {
    const sent: string[] = [];
    const releases: (() => void)[] = [];
    const input = new OrderedInput((data) => {
      sent.push(data);
      return new Promise<void>((resolve) => releases.push(resolve));
    });

    input.push("a");
    input.push("b");
    input.push("c");
    expect(sent).toEqual(["a"]);

    releases.shift()!();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(sent).toEqual(["a", "bc"]);

    releases.shift()!();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(sent).toEqual(["a", "bc"]);
  });

  test("keeps going after a failed request", async () => {
    const sent: string[] = [];
    const errors: unknown[] = [];
    let fail = true;
    const input = new OrderedInput(async (data) => {
      sent.push(data);
      if (fail) {
        fail = false;
        throw new Error("offline");
      }
    }, (error) => errors.push(error));

    input.push("x");
    input.push("y");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(sent).toEqual(["x", "y"]);
    expect(errors).toHaveLength(1);
  });
});

describe("fitTerminalFontSize", () => {
  // Menlo-like cells are about 0.6 of the font size wide.
  test("fills the width in half-pixel steps", () => {
    // 80 columns in 1000px: 1000 / (80 * 0.6) = 20.8 -> capped at 20.
    expect(fitTerminalFontSize(1000, 80, 0.6)).toBe(TTY_MAX_FONT_SIZE);
    // 80 columns in 800px: 16.66 -> 16.5.
    expect(fitTerminalFontSize(800, 80, 0.6)).toBe(16.5);
    // 120 columns need a smaller font for the same width.
    expect(fitTerminalFontSize(800, 120, 0.6)).toBe(11);
  });
  test("never goes below the readable minimum on a phone", () => {
    expect(fitTerminalFontSize(380, 80, 0.6)).toBe(TTY_MIN_FONT_SIZE);
    expect(fitTerminalFontSize(0, 80, 0.6)).toBe(TTY_MIN_FONT_SIZE);
  });
});
