import {describe, expect, test} from "bun:test";
import {highlightCode, languageForPath, MAX_HIGHLIGHT_CHARS} from "./code-highlight";

describe("languageForPath", () => {
  test("from the extension, lowlight's aliases included", () => {
    expect(languageForPath("/w/main.go")).toBe("go");
    expect(languageForPath("chat/src/a.tsx")).toBe("tsx");
    expect(languageForPath("config.YML")).toBe("yml");
    expect(languageForPath("pyproject.toml")).toBe("toml");
    expect(languageForPath("page.mdx")).toBe("markdown");
    expect(languageForPath("App.vue")).toBe("xml");
  });

  test("from well-known names", () => {
    expect(languageForPath("Makefile")).toBe("makefile");
    expect(languageForPath("/w/.env.local")).toBe("bash");
    expect(languageForPath("Gemfile")).toBe("ruby");
  });

  test("plain text otherwise", () => {
    expect(languageForPath("notes.txt")).toBeUndefined();
    expect(languageForPath("go.sum")).toBeUndefined();
    expect(languageForPath("LICENSE")).toBeUndefined();
  });
});

describe("highlightCode", () => {
  test("splits highlighted code into lines, keeping the classes of a token that spans lines", () => {
    const lines = highlightCode("const a = 1;\n/* one\ntwo */\n", "typescript");
    expect(lines).toHaveLength(3);
    expect(lines[0][0]).toEqual({text: "const", classes: ["hljs-keyword"]});
    expect(lines[1]).toEqual([{text: "/* one", classes: ["hljs-comment"]}]);
    expect(lines[2]).toEqual([{text: "two */", classes: ["hljs-comment"]}]);
  });

  test("nested tokens list their classes outermost first", () => {
    const [line] = highlightCode("const s = `a${b}`;", "typescript");
    expect(line).toContainEqual({text: "${b}", classes: ["hljs-string", "hljs-subst"]});
  });

  test("plain text without a language, and keeps empty lines", () => {
    expect(highlightCode("a\r\n\nb", undefined)).toEqual([[{text: "a", classes: []}], [], [{text: "b", classes: []}]]);
    expect(highlightCode("", undefined)).toEqual([[]]);
    expect(highlightCode("x", "no-such-language")).toEqual([[{text: "x", classes: []}]]);
  });

  test("large files stay plain", () => {
    const code = "const a = 1;\n".repeat(Math.ceil(MAX_HIGHLIGHT_CHARS / 13) + 1);
    expect(highlightCode(code, "typescript")[0]).toEqual([{text: "const a = 1;", classes: []}]);
  });
});
