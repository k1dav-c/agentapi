import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {RetryWait} from "@/lib/chat-api";
import {RetryWaitCard} from "./retry-card";

const now = Date.parse("2026-11-07T10:00:00Z");
const wait: RetryWait = {
  message: "Claude Code: the turn failed (api_error), API status 502: API Error: 502 Bad Gateway. This is a server-side issue, usually temporary — try again in a moment.",
  api_status: 502,
  stopped_at: "2026-11-07T09:59:58Z",
  attempt: 1,
  attempts: 3,
  retry_at: "2026-11-07T10:00:28Z",
};

describe("RetryWaitCard", () => {
  test("says what failed and counts down to the retry", () => {
    const html = renderToStaticMarkup(<RetryWaitCard retryWait={wait} queued={0} now={now} />);
    expect(html).toContain("data-retry-wait");
    expect(html).toContain(">Retrying</span>");
    expect(html).toContain("The API returned 502</h2>");
    expect(html).toContain("API Error: 502 Bad Gateway");
    expect(html).toContain("Retrying in 0:28 (1 of 3).");
    expect(html).toContain(">Retry now</button>");
    expect(html).toContain(">Cancel</button>");
    expect(html).toContain('title="Don&#x27;t try the task again"');
  });

  test("says that the queued tasks wait for it", () => {
    const html = renderToStaticMarkup(<RetryWaitCard retryWait={wait} queued={2} now={now} />);
    expect(html).toContain("Retrying in 0:28 (1 of 3). 2 queued tasks wait for it.");
    expect(html).toContain('title="Don&#x27;t try the task again; send the queued tasks"');
  });

  test("shows an error and keeps the buttons from acting twice", () => {
    const html = renderToStaticMarkup(<RetryWaitCard retryWait={wait} queued={0} now={now} busy error="the agent isn't running" />);
    expect(html).toContain("the agent isn&#x27;t running");
    expect(html.match(/disabled=""/g)).toHaveLength(2);
  });
});
