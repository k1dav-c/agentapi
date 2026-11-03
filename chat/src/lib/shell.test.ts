import { describe, expect, test } from "bun:test";
import { OrderedInput, decodeBase64, terminalSize } from "./shell";

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
