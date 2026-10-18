import {describe, expect, test} from "bun:test";
import {discoverFiles, discoverLinks, pathPattern, reconstructWrappedURLs} from "./explorer-index";

describe("session explorer URL reconstruction", () => {
  test("joins terminal-wrapped URL segments", () => {
    expect(
      reconstructWrappedURLs(
        "Open https://example.com/a/very/long/path?query=one&\nvalue=two",
      ),
    ).toEqual(["https://example.com/a/very/long/path?query=one&value=two"]);
  });

  test("leaves out the Markdown around a link, and links cut short", () => {
    expect(reconstructWrappedURLs("See **https://example.com/docs**, `https://example.com/a` and <https://example.com/b>.")).toEqual([
      "https://example.com/docs",
      "https://example.com/a",
      "https://example.com/b",
    ]);
    expect(reconstructWrappedURLs("Here is what I did for **Compare it with https://github.com/coder/agent…**:")).toEqual([]);
    expect(reconstructWrappedURLs("Read https://example.com/a_b and https://example.com/~user/x.")).toEqual([
      "https://example.com/a_b",
      "https://example.com/~user/x",
    ]);
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

describe("the Explorer's index", () => {
  const conversation = [
    {role: "agent", content: "Before any task: https://example.com/start"},
    {role: "user", content: "Look at /src/app/main.go"},
    {role: "agent", content: "Edited /src/app/main.go, see https://example.com/docs"},
    {role: "user", content: "And https://example.com/docs again, then /src/lib/util.ts."},
  ];

  test("lists each link and file once, with the task it first appears in", () => {
    expect(discoverLinks(conversation)).toEqual([
      {url: "https://example.com/start", task: 0},
      {url: "https://example.com/docs", task: 1},
    ]);
    expect(discoverFiles(conversation)).toEqual([
      {path: "/src/app/main.go", sourceTask: 1},
      {path: "/src/lib/util.ts", sourceTask: 2},
    ]);
  });

  test("reads a message once, and a new message again", () => {
    let reads = 0;
    const message = {role: "agent", get content() { reads++; return "https://example.com/once /a/b.go"; }};
    discoverLinks([message]);
    discoverLinks([message]);
    discoverFiles([message]);
    discoverFiles([message]);
    expect(reads).toBe(2);
    // A streamed message is a new object, so it is read again.
    expect(discoverLinks([{role: "agent", content: "https://example.com/more"}])).toEqual([{url: "https://example.com/more", task: 0}]);
  });
});
