import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {ChatErrorBoundary, ChatErrorScreen} from "./chat-error-boundary";

describe("ChatErrorBoundary", () => {
  test("an error replaces the chat with the error screen", () => {
    const error = new TypeError("message.content is not iterable");
    const state = ChatErrorBoundary.getDerivedStateFromError(error);
    expect(state).toEqual({failed: true, error});
    const boundary = new ChatErrorBoundary({children: <p>chat</p>});
    expect(renderToStaticMarkup(<>{boundary.render()}</>)).toBe("<p>chat</p>");
    boundary.state = state;
    expect(renderToStaticMarkup(<>{boundary.render()}</>)).toContain("data-chat-error");
  });
});

describe("ChatErrorScreen", () => {
  test("shows the error and offers to clear the cached transcript", () => {
    const html = renderToStaticMarkup(<ChatErrorScreen error={new TypeError("message.content is not iterable")} />);
    expect(html).toContain('role="alert"');
    expect(html).toMatch(/data-error-text[^>]*>message\.content is not iterable</);
    expect(html).toContain("Clear the cached transcript and reload");
    expect(html).toContain("Reload</button>");
  });

  test("shows what was thrown when it isn't an Error", () => {
    expect(renderToStaticMarkup(<ChatErrorScreen error="bad state" />)).toContain("&quot;bad state&quot;");
  });
});
