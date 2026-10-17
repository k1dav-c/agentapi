import {describe, expect, test} from "bun:test";
import {classifyDiffLines, deleteDiff, diffHeader, diffLines, editDiff, textLines, writeDiff} from "./diff";

const kinds = (diff: string) => classifyDiffLines(diff).map((line) => line.kind);

describe("textLines", () => {
  test("drops only the empty line after a final newline", () => {
    expect(textLines("a\nb\n")).toEqual(["a", "b"]);
    expect(textLines("a\r\nb")).toEqual(["a", "b"]);
    expect(textLines("a\n\n")).toEqual(["a", ""]);
    expect(textLines("")).toEqual([]);
  });
});

describe("diffHeader", () => {
  test("names both sides as git does", () => {
    expect(diffHeader("lib/a.go", "lib/a.go")).toBe("--- a/lib/a.go\n+++ b/lib/a.go");
    expect(diffHeader("/work/a.py", "/work/b.py")).toBe("--- a/work/a.py\n+++ b/work/b.py");
    expect(diffHeader(null, "new.txt")).toBe("--- /dev/null\n+++ b/new.txt");
    expect(diffHeader("old.txt", null)).toBe("--- a/old.txt\n+++ /dev/null");
  });
});

describe("diffLines", () => {
  test("keeps lines both sides have as context", () => {
    expect(diffLines("def add(a, b):\n    return a + b\n", "def add(a, b):\n    return a + b\n\ndef sub(a, b):\n    return a - b\n")).toEqual([
      " def add(a, b):",
      "     return a + b",
      "+",
      "+def sub(a, b):",
      "+    return a - b",
    ]);
  });

  test("puts the removed lines of a change before the added ones", () => {
    expect(diffLines("a\nb\nc\nd", "a\nB\nC\nd")).toEqual([" a", "-b", "-c", "+B", "+C", " d"]);
    expect(diffLines("one\ntwo\nthree", "one\nthree\nfour")).toEqual([" one", "-two", " three", "+four"]);
  });

  test("an empty side only adds or removes", () => {
    expect(diffLines("", "x\ny")).toEqual(["+x", "+y"]);
    expect(diffLines("x", "")).toEqual(["-x"]);
  });
});

describe("building diffs", () => {
  test("editDiff has one hunk per edit, without line numbers", () => {
    expect(editDiff("/w/a.ts", [{oldText: "let x = 1;", newText: "let x = 2;"}, {oldText: "f()", newText: "g()"}])).toBe(
      "--- a/w/a.ts\n+++ b/w/a.ts\n@@\n-let x = 1;\n+let x = 2;\n@@\n-f()\n+g()",
    );
  });

  test("writeDiff adds every line", () => {
    expect(writeDiff("a.txt", "one\ntwo\n")).toBe("--- a/a.txt\n+++ b/a.txt\n@@\n+one\n+two");
    expect(writeDiff("a.txt", "one\n", true)).toBe("--- /dev/null\n+++ b/a.txt\n@@ -0,0 +1,1 @@\n+one");
    expect(writeDiff("a.txt", "")).toBe("--- a/a.txt\n+++ b/a.txt");
  });

  test("deleteDiff removes the lines it knows", () => {
    expect(deleteDiff("a.txt", "x\ny")).toBe("--- a/a.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-x\n-y");
    expect(deleteDiff("a.txt")).toBe("--- a/a.txt\n+++ /dev/null");
  });
});

describe("classifyDiffLines", () => {
  test("a git diff", () => {
    const diff = [
      "diff --git a/calc.py b/calc.py",
      "index 3b18e51..a9c5f0d 100644",
      "--- a/calc.py",
      "+++ b/calc.py",
      "@@ -1,2 +1,3 @@ def add(a, b):",
      " def add(a, b):",
      "-    return a+b",
      "+    return a + b",
      "+",
      "\\ No newline at end of file",
    ].join("\n");
    expect(kinds(diff)).toEqual(["header", "header", "header", "header", "hunk", "context", "del", "add", "add", "meta"]);
  });

  test("a hunk with line counts ends after them, even at a --- line", () => {
    const diff = [
      "--- a/q.sql",
      "+++ b/q.sql",
      "@@ -1,2 +1,2 @@",
      "--- a comment",
      "+++ another",
      " select 1;",
      "diff --git a/b.txt b/b.txt",
      "--- a/b.txt",
      "+++ b/b.txt",
      "@@ -1 +1 @@",
      "-x",
      "+y",
    ].join("\n");
    expect(kinds(diff)).toEqual(["header", "header", "hunk", "del", "add", "context", "header", "header", "header", "hunk", "del", "add"]);
  });

  test("a hunk without counts ends at the next file's header", () => {
    const diff = "--- a/a.go\n+++ b/a.go\n@@\n-a\n+b\n\n--- a/b.go\n+++ b/b.go\n@@ func f()\n context";
    expect(kinds(diff)).toEqual(["header", "header", "hunk", "del", "add", "context", "header", "header", "hunk", "context"]);
  });

  test("text around a diff is neither a header nor a change", () => {
    expect(kinds("Moved to: x.go\n--- a/x\n+++ b/x\n@@\n+y\nnot part of it")).toEqual(["meta", "header", "header", "hunk", "add", "meta"]);
  });
});
