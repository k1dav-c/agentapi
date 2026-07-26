import {describe, expect, test} from "bun:test";
import {reconstructWrappedURLs} from "./explorer";

describe("session explorer URL reconstruction", () => {
  test("joins terminal-wrapped URL segments", () => {
    expect(
      reconstructWrappedURLs(
        "Open https://example.com/a/very/long/path?query=one&\nvalue=two",
      ),
    ).toEqual(["https://example.com/a/very/long/path?query=one&value=two"]);
  });

  test("does not join ordinary following prose", () => {
    expect(
      reconstructWrappedURLs("See https://example.com/docs\nThis is another line"),
    ).toEqual(["https://example.com/docs"]);
  });
});
