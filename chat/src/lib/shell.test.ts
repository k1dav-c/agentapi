import { describe, expect, test } from "bun:test";
import { decodeBase64, terminalSize } from "./shell";

describe("shell", () => {
  test("decodes output, escape sequences and UTF-8 included", () => {
    const bytes = decodeBase64(btoa("\x1b[32m$ \x1b[0m") + "");
    expect(Array.from(bytes.slice(0, 3))).toEqual([0x1b, 0x5b, 0x33]);
    expect(new TextDecoder().decode(decodeBase64("5L2g5aW9"))).toBe("你好");
  });

  test("sizes the terminal to its panel", () => {
    expect(terminalSize(800, 480, 8, 16)).toEqual({ cols: 100, rows: 30 });
    expect(terminalSize(100, 40, 8, 16)).toEqual({ cols: 20, rows: 5 });
    expect(terminalSize(800, 480, 0, 16)).toBeNull();
    expect(terminalSize(0, 480, 8, 16)).toBeNull();
  });
});
