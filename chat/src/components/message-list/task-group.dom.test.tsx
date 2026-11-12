import {afterEach, describe, expect, test} from "bun:test";
import {withDOM} from "@/test/dom";
import {mount} from "@/test/render";
import {AgentTurn, isReplyLoading} from "./task-group";

withDOM();

let view: Awaited<ReturnType<typeof mount>> | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
});

const placeholder = (container: HTMLElement) => container.querySelector("[data-reply-loading]");

describe("AgentTurn", () => {
  test("while the reply is on its way, it says so under the agent", async () => {
    view = await mount(<AgentTurn agentType="claude" replyLoading />);
    expect(view.container.querySelector("[data-agent-turn]")?.textContent).toBe("Claude Code");
    expect(placeholder(view.container)?.textContent).toBe("Loading the reply…");
    // The list says it to screen readers once, not each task.
    expect(placeholder(view.container)?.getAttribute("aria-hidden")).toBe("true");
    expect(placeholder(view.container)?.querySelector("svg")?.getAttribute("class")).toContain("motion-safe:animate-spin");
  });

  test("otherwise only the agent", async () => {
    view = await mount(<AgentTurn agentType="codex" />);
    expect(view.container.querySelector("[data-agent-turn]")?.textContent).toBe("Codex");
    expect(placeholder(view.container)).toBeNull();
  });
});

describe("isReplyLoading", () => {
  const quiet = {replaying: true, deferred: false, live: false, waitingForUser: false, steps: 0};

  test("a task with nothing under its prompt while the replay arrives", () => {
    expect(isReplyLoading(quiet)).toBe(true);
  });

  test("not once the replay is complete: a task without rich activity shows its prompt alone", () => {
    expect(isReplyLoading({...quiet, replaying: false})).toBe(false);
  });

  test("not a task with steps, a deferred one, the running one or the one waiting on you", () => {
    expect(isReplyLoading({...quiet, steps: 1})).toBe(false);
    expect(isReplyLoading({...quiet, deferred: true})).toBe(false);
    expect(isReplyLoading({...quiet, live: true})).toBe(false);
    expect(isReplyLoading({...quiet, waitingForUser: true})).toBe(false);
  });
});
