import {describe, expect, test} from "bun:test";
import {clampListWidth, keyListWidth, maxListWidth, parseListWidth} from "./hub-list-width";

describe("clampListWidth", () => {
  test("keeps a width that fits, in whole pixels", () => {
    expect(clampListWidth(360, 1440)).toBe(360);
    expect(clampListWidth(421.6, 1440)).toBe(422);
  });

  test("at least 280px, at most 640px and half the window", () => {
    expect(clampListWidth(100, 1440)).toBe(280);
    expect(clampListWidth(900, 1920)).toBe(640);
    expect(clampListWidth(600, 1000)).toBe(500);
    expect(clampListWidth(600, 1001)).toBe(500);
    expect(maxListWidth(768)).toBe(384);
  });

  test("a window too narrow for half of it still gets the narrowest list", () => {
    expect(maxListWidth(500)).toBe(280);
    expect(clampListWidth(360, 500)).toBe(280);
  });
});

describe("parseListWidth", () => {
  test("a stored width, clamped where it is used", () => {
    expect(parseListWidth("420")).toBe(420);
    expect(parseListWidth("5000")).toBe(5000);
  });

  test("nothing stored, or anything but a positive number, is the default", () => {
    for (const stored of [null, "", " ", "abc", "12px", "NaN", "Infinity", "-20", "0", "[400]", "{}"]) {
      expect(parseListWidth(stored)).toBe(360);
    }
  });
});

describe("keyListWidth", () => {
  test("the arrows move it 16px, 64px with Shift, within the bounds", () => {
    expect(keyListWidth({key: "ArrowRight"}, 360, 1440)).toBe(376);
    expect(keyListWidth({key: "ArrowLeft"}, 360, 1440)).toBe(344);
    expect(keyListWidth({key: "ArrowRight", shiftKey: true}, 360, 1440)).toBe(424);
    expect(keyListWidth({key: "ArrowLeft", shiftKey: true}, 300, 1440)).toBe(280);
    expect(keyListWidth({key: "ArrowRight"}, 630, 1440)).toBe(640);
    expect(keyListWidth({key: "ArrowRight"}, 380, 768)).toBe(384);
  });

  test("Home and End go to the narrowest and widest, Enter to the default", () => {
    expect(keyListWidth({key: "Home"}, 500, 1440)).toBe(280);
    expect(keyListWidth({key: "End"}, 360, 1440)).toBe(640);
    expect(keyListWidth({key: "End"}, 360, 1000)).toBe(500);
    expect(keyListWidth({key: "Enter"}, 500, 1440)).toBe(360);
  });

  test("other keys, and the browser's with Alt, Ctrl or Meta, are left alone", () => {
    expect(keyListWidth({key: "ArrowUp"}, 360, 1440)).toBeUndefined();
    expect(keyListWidth({key: "j"}, 360, 1440)).toBeUndefined();
    expect(keyListWidth({key: "ArrowLeft", altKey: true}, 360, 1440)).toBeUndefined();
    expect(keyListWidth({key: "ArrowRight", ctrlKey: true}, 360, 1440)).toBeUndefined();
    expect(keyListWidth({key: "Home", metaKey: true}, 360, 1440)).toBeUndefined();
  });
});
