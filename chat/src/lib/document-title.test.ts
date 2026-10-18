import {describe, expect, test} from "bun:test";
import {getDocumentTitle} from "./document-title";

describe("getDocumentTitle", () => {
  test("shows the running task", () => {
    expect(
      getDocumentTitle({
        agentState: "working",
        task: "Implement dynamic browser titles",
      }),
    ).toBe("● Working · Implement dynamic browser titles — AgentAPI");
  });

  test("leads with Needs you while the agent waits for the user", () => {
    expect(
      getDocumentTitle({
        agentState: "needs-you",
        task: "Rebuild the chat UI",
      }),
    ).toBe("◆ Needs you · Rebuild the chat UI — AgentAPI");
  });

  test("shows the connection state", () => {
    expect(
      getDocumentTitle({
        agentState: "offline",
        task: "Current task",
      }),
    ).toBe("○ Offline · Current task — AgentAPI");
  });

  test("normalizes and truncates long tasks", () => {
    const title = getDocumentTitle({
      agentState: "ready",
      task: `  ${"task ".repeat(20)} `,
    });

    expect(title).toStartWith("✓ Ready · task task");
    expect(title).toContain("… — AgentAPI");
  });

  test("uses a default title before a task is available", () => {
    expect(
      getDocumentTitle({
        agentState: "reconnecting",
      }),
    ).toBe("↻ Reconnecting · AgentAPI — Live Agent Session");
  });

  test("a custom title replaces the product name", () => {
    expect(getDocumentTitle({agentState: "ready", customTitle: "Docs bot"})).toBe("✓ Ready · Docs bot");
    expect(getDocumentTitle({agentState: "working", task: "Fix", customTitle: "Docs bot"})).toBe("● Working · Fix — Docs bot");
  });
});
