import {describe, expect, test} from "bun:test";
import {getDocumentTitle} from "./document-title";

describe("getDocumentTitle", () => {
  test("shows the running task", () => {
    expect(
      getDocumentTitle({
        connectionStatus: "connected",
        serverStatus: "running",
        task: "Implement dynamic browser titles",
      }),
    ).toBe("● Working · Implement dynamic browser titles — AgentAPI");
  });

  test("an open prompt reads as needing the user, even while stable", () => {
    expect(
      getDocumentTitle({
        connectionStatus: "connected",
        serverStatus: "stable",
        terminalPrompt: "Do you want to proceed?\n❯ 1. Yes",
        task: "Rebuild the chat UI",
      }),
    ).toBe("◆ Needs you · Rebuild the chat UI — AgentAPI");
  });

  test("prioritizes connection state over agent state", () => {
    expect(
      getDocumentTitle({
        connectionStatus: "offline",
        serverStatus: "running",
        task: "Current task",
      }),
    ).toBe("○ Offline · Current task — AgentAPI");
  });

  test("normalizes and truncates long tasks", () => {
    const title = getDocumentTitle({
      connectionStatus: "connected",
      serverStatus: "stable",
      task: `  ${"task ".repeat(20)} `,
    });

    expect(title).toStartWith("✓ Ready · task task");
    expect(title).toContain("… — AgentAPI");
  });

  test("uses a default title before a task is available", () => {
    expect(
      getDocumentTitle({
        connectionStatus: "reconnecting",
        serverStatus: "unknown",
      }),
    ).toBe("↻ Reconnecting · AgentAPI — Live Agent Session");
  });
});
