import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {ProcessedMessage} from "./processed-message";
import {FileViewerContext} from "./file-viewer-context";
import {TooltipProvider} from "./ui/tooltip";

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

  test("a Markdown link's label with Chinese punctuation keeps its URL", () => {
    const html = render("[mITXPC：Dynatron A26（180 W、65 mm）](https://mitxpc.com/products/a26)", true);
    expect(html).toContain('href="https://mitxpc.com/products/a26"');
    expect(html).toContain(">mITXPC：Dynatron A26（180 W、65 mm）</a>");
  });

  test.each([
    ["URL", "見 https://example.com/path，下一段", "https://example.com/path"],
    ["www. address", "見 www.example.com/path，下一段", "http://www.example.com/path"],
  ])("a bare %s in Markdown stops at Chinese punctuation", (_name, content, expectedHref) => {
    const html = render(content, true);
    expect(html).toContain(`href="${expectedHref}"`);
    expect(html).toContain("</a>，下一段");
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

  test("renders blockquotes with a visible rule", () => {
    const html = render("> Important context", true);
    expect(html).toContain("<blockquote");
    expect(html).toContain("border-l-2");
  });

  test("renders unordered and ordered lists with visible markers", () => {
    const html = render("- First\n- Second\n\n1. One\n2. Two", true);
    expect(html).toContain("<ul");
    expect(html).toContain("list-disc");
    expect(html).toContain("<ol");
    expect(html).toContain("list-decimal");
    expect(html).toContain("marker:text-muted-foreground");
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

describe("ProcessedMessage file paths", () => {
  const viewer = {open: () => undefined};
  const renderLinked = (content: string, linkFilePaths = true, withViewer = true) =>
    renderToStaticMarkup(
      <TooltipProvider>
        <FileViewerContext.Provider value={withViewer ? viewer : null}>
          <ProcessedMessage messageContent={content} isUser={false} renderMode="markdown" linkFilePaths={linkFilePaths} />
        </FileViewerContext.Provider>
      </TooltipProvider>,
    );

  test("inline code that names a file opens it", () => {
    const html = renderLinked("See `lib/httpapi/files.go:42` and `snake.html`.");
    expect(html).toContain('role="button"');
    expect(html).toContain('aria-label="Open file at line 42: lib/httpapi/files.go:42"');
    expect(html).toContain('aria-label="Open file: snake.html"');
    expect(html).toContain('aria-haspopup="dialog"');
  });

  test("a Markdown link to a file opens it instead of going to the page's address", () => {
    const html = renderLinked("See [the table](/home/me/research/%E5%B0%8D%E7%85%A7.csv) and [the docs](https://example.com/docs).");
    expect(html).toContain('title="Open /home/me/research/對照.csv"');
    expect(html).toContain('aria-haspopup="dialog"');
    expect(html).not.toContain('href="/home/me/research');
    expect(html).toContain('href="https://example.com/docs"');
    // Without the file viewer, the label alone.
    expect(renderLinked("[the table](/home/me/a.csv)", true, false)).not.toContain("<a");
  });

  test("other inline code and fenced code stay plain", () => {
    const html = renderLinked("Run `go test ./...` with `-v`.\n\n```\nlib/httpapi/files.go\n```\n\n```go\n// main.go\n```");
    expect(html).not.toContain('role="button"');
    expect(html).toContain("lib/httpapi/files.go");
  });

  test("a path in a link stays plain code", () => {
    const html = renderLinked("Open [`src/app.ts`](https://example.com/app) or `src/app.ts`.");
    expect(html.match(/role="button"/g)?.length).toBe(1);
    expect(html).toContain('<a href="https://example.com/app"');
  });

  test("only where asked for, with a viewer", () => {
    expect(renderLinked("`lib/a.go`", false)).not.toContain('role="button"');
    expect(renderLinked("`lib/a.go`", true, false)).not.toContain('role="button"');
    expect(render("`lib/a.go`", true)).not.toContain('role="button"');
  });
});
