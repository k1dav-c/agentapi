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
    expect(html).toContain("whitespace-pre");
    expect(html).toContain("overflow-x-auto");
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
});
