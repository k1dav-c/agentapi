import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {json, restoreFetch, routeFetch} from "@/test/fetch";
import {click, key, mount, settle} from "@/test/render";
import type {ActivityDay} from "@/lib/hub-activity";
import {ActivityCard} from "./activity-card";

withDOM();

afterEach(() => {
  restoreFetch();
  localStorage.clear();
});

const day = (date: string, work: number, usd: number, tokens: number): ActivityDay => ({
  date,
  work_seconds: work,
  usd,
  tokens: {input: tokens, output: 0, cache_read: 0, cache_write: 0, total: tokens},
});

// A year to Tuesday 2026-11-10, kept since the 3rd: the 9th busy in work,
// the 10th in tokens and spend.
function year(): ActivityDay[] {
  const end = Date.parse("2026-11-10T00:00:00Z");
  return Array.from({length: 371}, (_, i) => {
    const date = new Date(end - (370 - i) * 86_400_000).toISOString().slice(0, 10);
    if (date === "2026-11-09") return day(date, 7200, 1, 1000);
    if (date === "2026-11-10") return day(date, 330, 20, 2_000_000);
    if (date >= "2026-11-03") return day(date, 60 * (Number(date.slice(8)) - 2), 0.5, 100);
    return day(date, 0, 0, 0);
  });
}

describe("the Activity card", () => {
  test("reads the activity in the browser's time zone and shades by the measure picked, kept in the browser", async () => {
    const calls = routeFetch({"/hub/api/activity": () => json({days: year(), since: "2026-11-03", time_zone: "UTC"})});
    const card = await mount(<ActivityCard root="/hub" />);
    await settle();
    expect(calls).toHaveLength(1);
    expect(new URL(calls[0].url, "http://hub.test").searchParams.get("tz")).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);

    const cell = (date: string) => card.container.querySelector<HTMLElement>(`[aria-label^="${new Date(`${date}T00:00:00Z`).toLocaleDateString("en-US", {weekday: "short", month: "short", day: "numeric", year: "numeric", timeZone: "UTC"})}"]`)!;
    expect(card.container.querySelectorAll('[role="gridcell"]')).toHaveLength(52 * 7 + 3);
    expect(cell("2026-11-09").dataset.level).toBe("4");
    expect(cell("2026-11-10").dataset.level).toBe("3");
    expect(cell("2026-11-02").dataset.level).toBe("none");
    expect(cell("2026-11-10").getAttribute("aria-label")).toBe("Tue, Nov 10, 2026: worked 5m, 2M tokens, $20.00");

    await click([...card.container.querySelectorAll('[role="radio"]')].find((b) => b.textContent === "Spend"));
    expect(cell("2026-11-10").dataset.level).toBe("4");
    expect(localStorage.getItem("agentapi.hub.activity-metric")).toBe("spend");

    // One tab stop, today's; the arrows move it a day or a week.
    const today = cell("2026-11-10");
    expect(today.tabIndex).toBe(0);
    await act(async () => today.focus());
    expect(card.container.querySelector('[role="tooltip"]')?.textContent).toContain("Tue, Nov 10, 2026");
    expect(card.container.querySelector('[role="tooltip"]')?.textContent).toContain("Spend$20.00");
    await key("ArrowUp");
    expect(document.activeElement).toBe(cell("2026-11-09"));
    await key("ArrowLeft");
    expect(document.activeElement).toBe(cell("2026-11-02"));
    expect(card.container.querySelector('[role="tooltip"]')?.textContent).toContain("Before the hub kept its activity");
    await card.unmount();
  });

  test("reads it again when the page comes back into view", async () => {
    const calls = routeFetch({"/hub/api/activity": () => json({days: year(), since: "2026-11-03", time_zone: "UTC"})});
    const card = await mount(<ActivityCard root="/hub" />);
    await settle();
    const before = calls.length;
    await act(async () => void window.dispatchEvent(new Event("focus")));
    await settle();
    expect(calls.length).toBeGreaterThan(before);
    await card.unmount();
  });

  test("folded, it shows its title and the year's figure only", async () => {
    routeFetch({"/hub/api/activity": () => json({days: year(), since: "2026-11-03", time_zone: "UTC"})});
    let folded = true;
    const card = await mount(<ActivityCard root="/hub" fold={{folded, onToggle: () => (folded = !folded)}} />);
    await settle();
    expect(card.container.querySelector('[role="grid"]')).toBeNull();
    expect(card.container.textContent).toContain("2.4h in a year");
    const toggle = card.container.querySelector("button[aria-expanded]")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await click(toggle);
    expect(folded).toBe(false);
    await card.unmount();
  });
});
