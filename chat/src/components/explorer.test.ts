import {describe, expect, test} from "bun:test";
import {pathPattern, reconstructWrappedURLs} from "./explorer";

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

describe("session explorer file paths", () => {
  const paths = (text: string) => [...text.matchAll(pathPattern)].map((match) => match[1]);

  test("finds absolute paths in text and Markdown code", () => {
    expect(paths("Edited /work/lib/a.go:12 and `/work/docs/notes.md`.")).toEqual(["/work/lib/a.go", "/work/docs/notes.md"]);
    expect(paths("See /tmp/x.tar.gz.")).toEqual(["/tmp/x.tar.gz"]);
  });

  test("skips URLs and relative paths", () => {
    expect(paths("https://example.com/a.html and lib/a.go")).toEqual([]);
  });
});
