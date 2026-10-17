import {afterEach, describe, expect, test} from "bun:test";
import {copyText} from "./clipboard";

const realNavigator = globalThis.navigator;

function setNavigator(value: unknown) {
  Object.defineProperty(globalThis, "navigator", {value, configurable: true, writable: true});
}

// A document whose execCommand("copy") answers copied; records what was
// selected and whether the textarea was cleaned up.
function fakeDocument(copied: boolean) {
  const log = {selected: "", attached: 0, removed: 0};
  const area = {
    value: "",
    style: {} as Record<string, string>,
    setAttribute() {},
    select() {
      log.selected = area.value;
    },
    remove() {
      log.removed++;
    },
  };
  Object.assign(globalThis, {
    document: {
      createElement: () => area,
      body: {appendChild: () => log.attached++},
      execCommand: (command: string) => command === "copy" && copied,
    },
  });
  return log;
}

afterEach(() => {
  setNavigator(realNavigator);
  Reflect.deleteProperty(globalThis, "document");
});

describe("copyText", () => {
  test("uses the Clipboard API where there is one", async () => {
    const written: string[] = [];
    setNavigator({clipboard: {writeText: async (text: string) => void written.push(text)}});
    await copyText("claude auth login");
    expect(written).toEqual(["claude auth login"]);
  });

  test("copies through a selection on plain http", async () => {
    setNavigator({});
    const log = fakeDocument(true);
    await copyText("codex login");
    expect(log).toEqual({selected: "codex login", attached: 1, removed: 1});
  });

  test("falls back when the Clipboard API is blocked", async () => {
    setNavigator({clipboard: {writeText: async () => Promise.reject(new Error("NotAllowedError"))}});
    const log = fakeDocument(true);
    await copyText("pi");
    expect(log.selected).toBe("pi");
  });

  test("fails when neither works", async () => {
    setNavigator({});
    const log = fakeDocument(false);
    await expect(copyText("pi")).rejects.toThrow("Copying isn't available");
    expect(log.removed).toBe(1);
  });
});
