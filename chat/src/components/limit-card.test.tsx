import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {LimitStop} from "@/lib/chat-api";
import {LimitStopCard} from "./limit-card";

const now = Date.parse("2026-11-07T10:00:00Z");
const stop: LimitStop = {
  message: "Claude Code: You've hit your session limit.",
  stopped_at: "2026-11-07T09:59:00Z",
  resets_at: "2026-11-07T11:30:00Z",
};

describe("LimitStopCard", () => {
  test("says when the limit resets and offers to continue then", () => {
    const html = renderToStaticMarkup(<LimitStopCard limitStop={stop} queued={0} now={now} />);
    expect(html).toContain("data-limit-stop");
    expect(html).toContain(">Usage limit</span>");
    expect(html).toContain("The account reached its usage limit</h2>");
    expect(html).toContain("You&#x27;ve hit your session limit.");
    expect(html).toContain("The limit resets in 1h 30m, at ");
    expect(html).toContain(">Continue at reset</button>");
    expect(html).toContain(">Continue now</button>");
    expect(html).toContain('title="Hide this card"');
    expect(html).toContain(">Dismiss</button>");
  });

  test("says that the queued tasks wait, and lets them go", () => {
    const html = renderToStaticMarkup(<LimitStopCard limitStop={stop} queued={2} now={now} />);
    expect(html).toContain(" 2 queued tasks wait until then.");
    expect(html).toContain(">Send queued tasks</button>");
    const unknown = renderToStaticMarkup(<LimitStopCard limitStop={{...stop, resets_at: undefined}} queued={1} now={now} />);
    expect(unknown).toContain("The agent didn&#x27;t say when the limit resets. 1 queued task waits until you go on.");
    expect(unknown).not.toContain("Continue at reset");
  });

  test("a scheduled continuation can be cancelled", () => {
    const html = renderToStaticMarkup(<LimitStopCard limitStop={{...stop, continue_at: "2026-11-07T11:30:00Z"}} queued={1} now={now} />);
    expect(html).toContain(">Don&#x27;t continue</button>");
    expect(html).not.toContain("Continue at reset");
    expect(html).toContain("Continues the task at ");
    expect(html).toContain("1 queued task waits for the task to go on.");
  });

  test("shows why an action failed, and disables the buttons while one runs", () => {
    const html = renderToStaticMarkup(<LimitStopCard limitStop={stop} queued={0} now={now} busy error="Failed to continue the task" />);
    expect(html).toContain("Failed to continue the task");
    expect(html.match(/<button[^>]*disabled=""/g)?.length).toBe(3);
  });
});
