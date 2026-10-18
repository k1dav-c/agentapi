import {describe, expect, test} from "bun:test";
import {isRelativeRef, previewKind, resolveRelativeRef} from "./file-preview";

describe("previewKind", () => {
  test("by the file's extension", () => {
    expect(previewKind("site/index.html")).toBe("html");
    expect(previewKind("PAGE.HTM")).toBe("html");
    expect(previewKind("docs/README.md")).toBe("markdown");
    expect(previewKind("notes.markdown")).toBe("markdown");
    expect(previewKind("img/logo.PNG")).toBe("image");
    expect(previewKind("icon.svg")).toBe("image");
  });
  test("none for other files", () => {
    expect(previewKind("main.go")).toBeUndefined();
    expect(previewKind("Makefile")).toBeUndefined();
    expect(previewKind("archive.html.gz")).toBeUndefined();
  });
});

describe("isRelativeRef", () => {
  test("paths are relative", () => {
    expect(isRelativeRef("img/a.png")).toBe(true);
    expect(isRelativeRef("../a.md")).toBe(true);
    expect(isRelativeRef("/docs/a.md")).toBe(true);
  });
  test("URLs and fragments aren't", () => {
    for (const ref of ["https://example.com/a.png", "mailto:a@b.c", "data:image/png;base64,AA", "//cdn.example.com/a.js", "#usage", ""]) {
      expect(isRelativeRef(ref)).toBe(false);
    }
  });
});

describe("resolveRelativeRef", () => {
  test("from the document's directory", () => {
    expect(resolveRelativeRef("docs/guide/README.md", "img/shot.png")).toEqual({path: "docs/guide/img/shot.png"});
    expect(resolveRelativeRef("docs/guide/README.md", "./a.md")).toEqual({path: "docs/guide/a.md"});
    expect(resolveRelativeRef("docs/guide/README.md", "../../LICENSE")).toEqual({path: "LICENSE"});
    expect(resolveRelativeRef("README.md", "docs/a.md")).toEqual({path: "docs/a.md"});
  });
  test("a leading slash is the working directory", () => {
    expect(resolveRelativeRef("docs/README.md", "/img/a.png")).toEqual({path: "img/a.png"});
  });
  test("drops the query, decodes the path, and reads a line fragment", () => {
    expect(resolveRelativeRef("README.md", "my%20file.md?raw=1")).toEqual({path: "my file.md"});
    expect(resolveRelativeRef("README.md", "src/main.go#L42")).toEqual({path: "src/main.go", line: 42});
    expect(resolveRelativeRef("README.md", "docs/a.md#usage")).toEqual({path: "docs/a.md"});
  });
  test("nothing outside the working directory, and no URLs", () => {
    expect(resolveRelativeRef("README.md", "../secret.md")).toBeUndefined();
    expect(resolveRelativeRef("docs/a.md", "../../x.png")).toBeUndefined();
    expect(resolveRelativeRef("README.md", "https://example.com/a.png")).toBeUndefined();
    expect(resolveRelativeRef("README.md", "#usage")).toBeUndefined();
    expect(resolveRelativeRef("README.md", "?raw=1")).toBeUndefined();
  });
});
