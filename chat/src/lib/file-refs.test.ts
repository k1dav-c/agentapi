import {describe, expect, test} from "bun:test";
import {fileName, parseFilePathRef, toolFileTargets, viewerTab} from "./file-refs";

describe("parseFilePathRef", () => {
  test("paths with a directory or a file name", () => {
    expect(parseFilePathRef("snake.html")).toEqual({path: "snake.html"});
    expect(parseFilePathRef("lib/foo.go")).toEqual({path: "lib/foo.go"});
    expect(parseFilePathRef("/work/a/b.ts:12")).toEqual({path: "/work/a/b.ts", line: 12});
    expect(parseFilePathRef("./x.py:3:5")).toEqual({path: "./x.py", line: 3, col: 5});
    expect(parseFilePathRef("../up/notes.md")).toEqual({path: "../up/notes.md"});
    expect(parseFilePathRef("chat/src/app/[id]/page.tsx")).toEqual({path: "chat/src/app/[id]/page.tsx"});
    expect(parseFilePathRef("lib/httpapi/server_test.go:120")).toEqual({path: "lib/httpapi/server_test.go", line: 120});
    expect(parseFilePathRef("docs/筆記.md")).toEqual({path: "docs/筆記.md"});
    expect(parseFilePathRef("a/b/config.unknownext")).toEqual({path: "a/b/config.unknownext"});
  });

  test("well-known file names and dotfiles", () => {
    for (const name of ["package.json", "go.mod", "README.md", "Makefile", "Dockerfile", ".env", ".env.local", ".gitignore", ".golangci.yml", "foo.test.ts"]) {
      expect(parseFilePathRef(name)).toEqual({path: name});
    }
    expect(parseFilePathRef(".github/workflows/ci.yml")).toEqual({path: ".github/workflows/ci.yml"});
  });

  test("a line 0 is no line", () => {
    expect(parseFilePathRef("a.go:0")).toEqual({path: "a.go"});
  });

  test("not URLs, versions, flags or abbreviations", () => {
    for (const text of [
      "https://example.com/a.html",
      "localhost:3284",
      "127.0.0.1:8080",
      "github.com/coder/agentapi/main.go",
      "1.2.3",
      "v0.23.0",
      "-v",
      "--force",
      "e.g.",
      "i.e.",
      "12:30",
      "...",
    ]) {
      expect(parseFilePathRef(text)).toBeNull();
    }
  });

  test("not bare words, code or other names with a slash", () => {
    for (const text of [
      "foo",
      "README",
      "useChat()",
      "console.log",
      "React.memo",
      "fmt.Println",
      "window.location",
      "res.json",
      "Next.js",
      "foo.bar",
      "next/navigation",
      "@radix-ui/react-dialog",
      "@/lib/utils.ts",
      "text/html",
      "and/or",
      "lib/httpapi",
      "lib/httpapi/",
      "/files/content",
    ]) {
      expect(parseFilePathRef(text)).toBeNull();
    }
  });

  test("not commands, globs, variables or home paths", () => {
    for (const text of ["go test ./...", "*.go", "src/**/*.ts", "$HOME/a.sh", "~/a.txt", "<path>.go", "a//b.go", "x".repeat(298) + ".go", ""]) {
      expect(parseFilePathRef(text)).toBeNull();
    }
  });
});

describe("fileName", () => {
  test("the last segment", () => {
    expect(fileName("/work/lib/a.go")).toBe("a.go");
    expect(fileName("a.go")).toBe("a.go");
  });
});

describe("toolFileTargets: Claude Code", () => {
  test("Read starts at its offset", () => {
    expect(toolFileTargets("Read", {file_path: "/w/a.go"})).toEqual([{path: "/w/a.go", kind: "read"}]);
    expect(toolFileTargets("Read", {file_path: "/w/a.go", offset: 40, limit: 20})).toEqual([{path: "/w/a.go", kind: "read", line: 40}]);
  });

  test("Write adds its content", () => {
    expect(toolFileTargets("Write", {file_path: "/w/n.txt", content: "one\ntwo\n"})).toEqual([
      {path: "/w/n.txt", kind: "write", change: "--- a/w/n.txt\n+++ b/w/n.txt\n@@\n+one\n+two"},
    ]);
  });

  test("Edit and MultiEdit show each edit as a hunk", () => {
    expect(toolFileTargets("Edit", {file_path: "/w/calc.py", old_string: "a+b", new_string: "a + b", replace_all: false})).toEqual([
      {path: "/w/calc.py", kind: "edit", change: "--- a/w/calc.py\n+++ b/w/calc.py\n@@\n-a+b\n+a + b"},
    ]);
    expect(
      toolFileTargets("MultiEdit", {
        file_path: "/w/calc.py",
        edits: [{old_string: "x = 1\ny = 2", new_string: "x = 1\ny = 3"}, {old_string: "z", new_string: "", replace_all: true}],
      }),
    ).toEqual([{path: "/w/calc.py", kind: "edit", change: "--- a/w/calc.py\n+++ b/w/calc.py\n@@\n x = 1\n-y = 2\n+y = 3\n@@\n-z"}]);
  });

  test("NotebookEdit names the notebook", () => {
    expect(toolFileTargets("NotebookEdit", {notebook_path: "/w/a.ipynb", cell_id: "c1", new_source: "x"})).toEqual([{path: "/w/a.ipynb", kind: "edit"}]);
  });

  test("an input that arrived as JSON text", () => {
    expect(toolFileTargets("Read", '{"file_path":"/w/a.go"}')).toEqual([{path: "/w/a.go", kind: "read"}]);
  });

  test("tools without a file", () => {
    expect(toolFileTargets("Bash", {command: "ls"})).toEqual([]);
    expect(toolFileTargets("Grep", {pattern: "x", path: "/w"})).toEqual([]);
    expect(toolFileTargets("Glob", {pattern: "**/*.go"})).toEqual([]);
    expect(toolFileTargets("Read", {})).toEqual([]);
    expect(toolFileTargets("Edit", undefined)).toEqual([]);
  });
});

describe("toolFileTargets: Pi", () => {
  test("read, write and edit", () => {
    expect(toolFileTargets("read", {path: "hello.txt", offset: null, limit: null})).toEqual([{path: "hello.txt", kind: "read"}]);
    expect(toolFileTargets("write", {path: "n.txt", content: "hi"})).toEqual([{path: "n.txt", kind: "write", change: "--- a/n.txt\n+++ b/n.txt\n@@\n+hi"}]);
    expect(toolFileTargets("edit", {path: "hello.txt", edits: [{oldText: "hello from scratch", newText: "hello from pi"}]})).toEqual([
      {path: "hello.txt", kind: "edit", change: "--- a/hello.txt\n+++ b/hello.txt\n@@\n-hello from scratch\n+hello from pi"},
    ]);
    expect(toolFileTargets("edit", {path: "a.txt", oldText: "x", newText: "y"})).toEqual([
      {path: "a.txt", kind: "edit", change: "--- a/a.txt\n+++ b/a.txt\n@@\n-x\n+y"},
    ]);
  });
});

describe("toolFileTargets: Codex native", () => {
  test("apply_patch keeps app-server's hunks", () => {
    const input = {
      file_path: "/work/scratch/calc.py",
      changes: [{path: "/work/scratch/calc.py", kind: "update", diff: "@@ -2 +2,5 @@\n     return a + b\n+\n+\n+def sub(a, b):\n+    return a - b\n"}],
    };
    expect(toolFileTargets("apply_patch", input)).toEqual([
      {
        path: "/work/scratch/calc.py",
        kind: "edit",
        change: "--- a/work/scratch/calc.py\n+++ b/work/scratch/calc.py\n@@ -2 +2,5 @@\n     return a + b\n+\n+\n+def sub(a, b):\n+    return a - b",
      },
    ]);
  });

  test("added, deleted and moved files", () => {
    const input = {
      changes: [
        {path: "/w/new.txt", kind: "add", diff: "hello\nworld\n"},
        {path: "/w/old.txt", kind: {type: "delete"}, diff: "bye\n"},
        {path: "/w/a.go", kind: {type: "update", move_path: "/w/b.go"}, diff: "@@ -1 +1 @@\n-x\n+y\n"},
        {path: "/w/c.go", kind: "update", diff: "@@ -1 +1 @@\n-p\n+q\n\nMoved to: /w/d.go"},
      ],
    };
    expect(toolFileTargets("apply_patch", input)).toEqual([
      {path: "/w/new.txt", kind: "write", change: "--- /dev/null\n+++ b/w/new.txt\n@@ -0,0 +1,2 @@\n+hello\n+world"},
      {path: "/w/old.txt", kind: "delete", change: "--- a/w/old.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-bye"},
      {path: "/w/b.go", kind: "edit", movedFrom: "/w/a.go", change: "--- a/w/a.go\n+++ b/w/b.go\n@@ -1 +1 @@\n-x\n+y"},
      {path: "/w/d.go", kind: "edit", movedFrom: "/w/c.go", change: "--- a/w/c.go\n+++ b/w/d.go\n@@ -1 +1 @@\n-p\n+q"},
    ]);
  });

  test("view_image", () => {
    expect(toolFileTargets("view_image", {path: "/tmp/shot.png"})).toEqual([{path: "/tmp/shot.png", kind: "image"}]);
  });
});

describe("toolFileTargets: Codex patches", () => {
  const patch = [
    "*** Begin Patch",
    "*** Update File: calc.py",
    "@@",
    " def add(a, b):",
    "     return a + b",
    "+",
    "+def sub(a, b):",
    "+    return a - b",
    "*** Add File: docs/notes.md",
    "+# Notes",
    "+",
    "*** Delete File: old.txt",
    "*** Update File: a.go",
    "*** Move to: b.go",
    "@@ func f() {",
    "-\treturn 1",
    "+\treturn 2",
    "*** End of File",
    "*** End Patch",
  ].join("\n");
  const expected = [
    {path: "calc.py", kind: "edit", change: "--- a/calc.py\n+++ b/calc.py\n@@\n def add(a, b):\n     return a + b\n+\n+def sub(a, b):\n+    return a - b"},
    {path: "docs/notes.md", kind: "write", change: "--- /dev/null\n+++ b/docs/notes.md\n@@ -0,0 +1,2 @@\n+# Notes\n+"},
    {path: "old.txt", kind: "delete", change: "--- a/old.txt\n+++ /dev/null"},
    {path: "b.go", kind: "edit", movedFrom: "a.go", change: "--- a/a.go\n+++ b/b.go\n@@ func f() {\n-\treturn 1\n+\treturn 2"},
  ];

  test("the patch text of the PTY apply_patch", () => {
    expect(toolFileTargets("apply_patch", patch)).toEqual(expected);
    expect(toolFileTargets("apply_patch", {input: patch})).toEqual(expected);
  });

  test("a patch from a code-mode exec script", () => {
    const script = `const r = await tools.apply_patch(${JSON.stringify(patch)});\ntext(r);\n`;
    expect(toolFileTargets("exec", script)).toEqual(expected);
    expect(toolFileTargets("exec", `await tools.apply_patch({patch: ${JSON.stringify(patch)}});`)).toEqual(expected);
  });

  test("a patch through the shell", () => {
    expect(toolFileTargets("shell", {command: ["apply_patch", patch]})).toEqual(expected);
    expect(toolFileTargets("exec_command", {cmd: `apply_patch <<'EOF'\n${patch}\nEOF`})).toEqual(expected);
  });

  test("scripts and commands without a patch", () => {
    expect(toolFileTargets("exec", "text(await tools.exec_command({cmd:\"ls\"}));")).toEqual([]);
    expect(toolFileTargets("exec_command", {cmd: "rg --files"})).toEqual([]);
    expect(toolFileTargets("Bash", {command: `cat <<'EOF'\n${patch}\nEOF`})).toEqual([]);
  });
});

describe("viewerTab", () => {
  test("a change opens on its diff, a read on the file", () => {
    expect(viewerTab("edit")).toBe("diff");
    expect(viewerTab("write")).toBe("diff");
    expect(viewerTab("delete")).toBe("diff");
    expect(viewerTab("read")).toBe("file");
    expect(viewerTab("image")).toBe("file");
  });
});
