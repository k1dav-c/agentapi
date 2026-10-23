import {describe, expect, test} from "bun:test";
import {resolveAgentAPIUrl} from "./agentapi-url";

describe("resolveAgentAPIUrl", () => {
  test("the url parameter wins", () => {
    expect(resolveAgentAPIUrl("http://localhost:3284", "/chat", "https://example.com")).toBe("http://localhost:3284");
  });

  test("otherwise the server that serves the chat, under a subpath too", () => {
    expect(resolveAgentAPIUrl(null, "/chat", "https://example.com")).toBe("https://example.com");
    expect(resolveAgentAPIUrl(null, "/@admin/ws.agent/apps/ccw/chat", "https://coder.example.com")).toBe("https://coder.example.com/@admin/ws.agent/apps/ccw");
  });

  test("none without either", () => {
    expect(resolveAgentAPIUrl(null, undefined, "https://example.com")).toBeUndefined();
    expect(resolveAgentAPIUrl("", "", "https://example.com")).toBeUndefined();
  });
});
