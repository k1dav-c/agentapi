// The hub's activity: what the agents did each day of the last year, as
// the hub saw it (GET /api/activity), for the overview's heatmap. The hub
// keeps the hours; it adds up the days in the browser's time zone. A
// GitHub-style grid: a column a week, Sunday on top, today in the last
// column, each day shaded by one of five levels of the measure picked.

import {formatTokens, formatUSD} from "./format";
import type {HubWire} from "./hub";
import {hubCall} from "./hub-api";
import {formatWork} from "./hub-work";
import type {Fetcher} from "./hub-update";

export type ActivityDay = HubWire["ActivityDay"];
export type Activity = HubWire["ActivityBody"];

// ActivityMetric is what the heatmap's shades show.
export type ActivityMetric = "work" | "tokens" | "spend";

export const activityMetrics: {key: ActivityMetric; label: string}[] = [
  {key: "work", label: "Work time"},
  {key: "tokens", label: "Tokens"},
  {key: "spend", label: "Spend"},
];

export function parseActivityMetric(stored: string | null): ActivityMetric {
  return stored === "tokens" || stored === "spend" ? stored : "work";
}

// ACTIVITY_WEEKS is how many weeks the heatmap shows: a year and the week
// under way, which the hub's 371 days cover.
export const ACTIVITY_WEEKS = 53;

// metricValue is a day's figure of metric.
export function metricValue(day: ActivityDay, metric: ActivityMetric): number {
  switch (metric) {
    case "work":
      return day.work_seconds;
    case "tokens":
      return day.tokens.total;
    case "spend":
      return day.usd;
  }
}

// formatMetric is a figure of metric, as the overview's cards say it.
export function formatMetric(value: number, metric: ActivityMetric): string {
  switch (metric) {
    case "work":
      return formatWork(value);
    case "tokens":
      return formatTokens(value);
    case "spend":
      return formatUSD(value);
  }
}

// levelThresholds are the most of levels 1, 2 and 3, and the busiest
// day's figure, from the days with any: a quarter of them each, as GitHub
// shades. One busy day doesn't wash out the rest.
export function levelThresholds(values: number[]): [number, number, number, number] {
  const sorted = values.filter((value) => value > 0).sort((a, b) => a - b);
  if (sorted.length === 0) return [Infinity, Infinity, Infinity, Infinity];
  const at = (q: number) => sorted[Math.max(0, Math.ceil(q * sorted.length) - 1)];
  return [at(0.25), at(0.5), at(0.75), sorted[sorted.length - 1]];
}

// levelOf is a figure's shade: 0 for none, then 1 to 4 by the quarter of
// the days with any it is in; the busiest day is 4.
export function levelOf(value: number, thresholds: [number, number, number, number]): 0 | 1 | 2 | 3 | 4 {
  if (!(value > 0)) return 0;
  if (value >= thresholds[3]) return 4;
  if (value <= thresholds[0]) return 1;
  if (value <= thresholds[1]) return 2;
  if (value <= thresholds[2]) return 3;
  return 4;
}

// A cell of the heatmap: its date, and the day's figures, which a day the
// hub didn't keep yet (before since) or one to come hasn't.
export interface ActivityCell {
  date: string;
  day?: ActivityDay;
  // Before the hub kept its activity: nothing to say of it.
  before: boolean;
  // A day of this week still to come: not drawn.
  future: boolean;
}

// The heatmap: the weeks, each Sunday to Saturday, the earliest first, and
// the month labels by the column they start at.
export interface ActivityGrid {
  weeks: ActivityCell[][];
  months: {column: number; label: string}[];
}

const DAY_MS = 24 * 3600 * 1000;

// utcOf is a date (YYYY-MM-DD) as a time at midnight UTC, for counting
// days without the time zone's shifts.
const utcOf = (date: string) => Date.parse(`${date}T00:00:00Z`);
const dateOf = (time: number) => new Date(time).toISOString().slice(0, 10);

// activityGrid lays the activity's days out: today, its last day, in the
// last column.
export function activityGrid(activity: Activity, weeks = ACTIVITY_WEEKS): ActivityGrid {
  const byDate = new Map(activity.days.map((day) => [day.date, day]));
  const today = activity.days.at(-1)?.date ?? dateOf(Date.now());
  const todayTime = utcOf(today);
  const weekday = new Date(todayTime).getUTCDay();
  const first = todayTime - ((weeks - 1) * 7 + weekday) * DAY_MS;
  const grid: ActivityGrid = {weeks: [], months: []};
  let lastMonth = -1;
  for (let w = 0; w < weeks; w++) {
    const week: ActivityCell[] = [];
    for (let d = 0; d < 7; d++) {
      const time = first + (w * 7 + d) * DAY_MS;
      const date = dateOf(time);
      week.push({date, day: byDate.get(date), before: date < activity.since, future: time > todayTime});
    }
    grid.weeks.push(week);
    // A month is labeled at the first week that starts in it.
    const month = new Date(first + w * 7 * DAY_MS).getUTCMonth();
    if (month !== lastMonth) {
      grid.months.push({column: w, label: new Date(first + w * 7 * DAY_MS).toLocaleDateString("en-US", {month: "short", timeZone: "UTC"})});
      lastMonth = month;
    }
  }
  // The first label has no room when the next is close.
  if (grid.months.length > 1 && grid.months[1].column - grid.months[0].column < 3) grid.months.shift();
  return grid;
}

// formatDate is a day as the tooltip says it: "Tue, Nov 10, 2026".
export function formatDate(date: string): string {
  return new Date(utcOf(date)).toLocaleDateString("en-US", {weekday: "short", month: "short", day: "numeric", year: "numeric", timeZone: "UTC"});
}

// cellText says what a cell is, for its tooltip and label.
export function cellText(cell: ActivityCell): string {
  if (cell.before || !cell.day) return `${formatDate(cell.date)}: not recorded`;
  const day = cell.day;
  return `${formatDate(cell.date)}: worked ${formatWork(day.work_seconds)}, ${formatTokens(day.tokens.total)} tokens, ${formatUSD(day.usd)}`;
}

// activityTotal is the figure of metric over all the days.
export function activityTotal(activity: Activity, metric: ActivityMetric): number {
  return activity.days.reduce((sum, day) => sum + metricValue(day, metric), 0);
}

// parseActivity checks the hub's answer; undefined when it isn't one.
export function parseActivity(body: unknown): Activity | undefined {
  const value = body as Partial<Activity> | null;
  if (!value || !Array.isArray(value.days) || typeof value.since !== "string") return undefined;
  const days = value.days.filter(
    (day): day is ActivityDay =>
      !!day && typeof day.date === "string" && typeof day.work_seconds === "number" && typeof day.usd === "number" && typeof day.tokens?.total === "number",
  );
  return {days, since: value.since, time_zone: typeof value.time_zone === "string" ? value.time_zone : ""};
}

// browserTimeZone is the browser's IANA time zone; UTC when it doesn't say.
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

// fetchActivity reads the activity in timeZone; it throws HubAPIError.
export async function fetchActivity(root: string, timeZone: string, fetcher?: Fetcher): Promise<Activity> {
  const response = await hubCall(root, fetcher)(`/api/activity?tz=${encodeURIComponent(timeZone)}`);
  const activity = parseActivity(await response.json().catch(() => null));
  if (!activity) throw new Error("The hub's answer has no activity.");
  return activity;
}
