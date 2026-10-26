import {describe, expect, test} from "bun:test";
import {viewportFrame} from "./viewport-frame";

describe("viewportFrame", () => {
  test("a keyboard that shrinks the visual viewport (Chrome on iOS)", () => {
    expect(viewportFrame(852, {height: 447, offsetTop: 0})).toEqual({top: 0, height: 447});
  });
  test("a keyboard that shrinks the window and scrolls it to the field (measured on iOS 26)", () => {
    expect(viewportFrame(372, {height: 372, offsetTop: 345})).toEqual({top: 345, height: 372});
  });
  test("a window scrolled with no keyboard", () => {
    expect(viewportFrame(717, {height: 717, offsetTop: 120})).toEqual({top: 120, height: 717});
  });
  test("none at rest, as toolbars slide, zoomed in, or without a visual viewport", () => {
    expect(viewportFrame(717, {height: 717, offsetTop: 0})).toBeNull();
    expect(viewportFrame(844, {height: 790, offsetTop: 0})).toBeNull();
    expect(viewportFrame(852, {height: 300, offsetTop: 200, scale: 2})).toBeNull();
    expect(viewportFrame(852, null)).toBeNull();
  });
});
