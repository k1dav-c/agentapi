import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {ProcessedMessage} from "./processed-message";

function render(content: string, isUser = false) {
  return renderToStaticMarkup(
    <ProcessedMessage messageContent={content} isUser={isUser} />,
  );
}

describe("ProcessedMessage links", () => {
  test.each([
    ["balanced parentheses", "https://example.com/docs(foo).", "https://example.com/docs(foo)"],
    ["Chinese punctuation", "https://example.com/path，下一段", "https://example.com/path"],
    ["long query string", "https://example.com/search?first=one&second=two", "https://example.com/search?first=one&amp;second=two"],
    ["URL followed by text", "https://example.com/path, then", "https://example.com/path"],
  ])("%s", (_name, content, expectedHref) => {
    const html = render(content);
    expect(html).toContain(`href="${expectedHref}"`);
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
  });

  test("renders a Markdown link without re-linkifying its label", () => {
    const html = render("[OpenAI](https://openai.com/docs)", true);
    expect(html).toContain('href="https://openai.com/docs"');
    expect(html).toContain(">OpenAI</a>");
  });

  test("adds wrapping styles to long links", () => {
    const html = render("https://example.com/a-very-long-path", true);
    expect(html).toContain("break-words");
    expect(html).toContain("[overflow-wrap:anywhere]");
  });

  test("renders agent output as preformatted terminal text", () => {
    const html = render("first line\nsecond line");
    expect(html).toContain("whitespace-pre-wrap");
    expect(html).toContain("[overflow-wrap:anywhere]");
    expect(html).not.toContain("overflow-x-auto");
    expect(html).toContain("first line\nsecond line");
    expect(html).not.toContain("<br");
  });

  test("preserves repeated spaces used for terminal layout", () => {
    const html = render(
      "│ model: gpt-5.6-sol     /model to change │\n│ directory: ~/agentapi                       │",
    );
    expect(html).toContain("gpt-5.6-sol     /model");
    expect(html).toContain("~/agentapi                       │");
  });

  test("can render agent output as Markdown", () => {
    const html = renderToStaticMarkup(
      <ProcessedMessage
        messageContent="**Completed**"
        isUser={false}
        renderMode="markdown"
      />,
    );
    expect(html).toContain("<strong>Completed</strong>");
  });

  test("renders GFM tables with readable table structure", () => {
    const html = render(
      "| Name | Status |\n| --- | --- |\n| Build | Passed |",
      true,
    );
    expect(html).toContain("<table");
    expect(html).toContain("<thead");
    expect(html).toContain("<th");
    expect(html).toContain("<td");
    expect(html).toContain(">Build</td>");
    expect(html).toContain("overflow-x-auto");
  });

  test("renders fenced and inline code with visible contrast", () => {
    const html = render("Use `go test`.\n\n```go\nfunc main() {}\n```", true);
    expect(html).toContain("<pre");
    expect(html).toContain("bg-code-block-bg");
    expect(html).toContain("border-code-block-border");
    expect(html).toContain("bg-muted");
    expect(html).toContain("go test");
    // With syntax highlighting, "func main() {}" is split across <span> elements
    expect(html).toContain("func");
    expect(html).toContain("main");
  });

  test("renders blockquotes with a visible border and background", () => {
    const html = render("> Important context", true);
    expect(html).toContain("<blockquote");
    expect(html).toContain("border-l-4");
    expect(html).toContain("bg-muted/60");
  });

  test("renders unordered and ordered lists with visible markers", () => {
    const html = render("- First\n- Second\n\n1. One\n2. Two", true);
    expect(html).toContain("<ul");
    expect(html).toContain("list-disc");
    expect(html).toContain("<ol");
    expect(html).toContain("list-decimal");
    expect(html).toContain("marker:text-primary");
  });

  test("highlights case-insensitive raw output search matches", () => {
    const html = renderToStaticMarkup(
      <ProcessedMessage
        messageContent="Build complete. BUILD passed."
        isUser={false}
        searchQuery="build"
      />,
    );
    expect(html.match(/<mark/g)?.length).toBe(2);
    expect(html).toContain(">Build</mark>");
  });
});
