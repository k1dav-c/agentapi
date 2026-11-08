// The work time: how long the hub saw the instances working (the state
// working, while it followed them), in the last 24 hours and 7 days. The
// hub counts only what it saw: not while it was down, or couldn't reach an
// instance.

import {secondsBetween} from "./clock";
import type {HubInstance} from "./hub";

const DAY_MS = 24 * 3600 * 1000;
const WEEK_MS = 7 * DAY_MS;

export interface WorkSeconds {
  day: number;
  week: number;
}

// workSeconds is an instance's work time at now: what the hub sent, and the
// span going on, within each window.
export function workSeconds(instance: HubInstance, now: number): WorkSeconds {
  const work = instance.work;
  if (!work) return {day: 0, week: 0};
  const seconds = {day: work.last_24h_seconds, week: work.last_7d_seconds};
  const since = work.working_since ? Date.parse(work.working_since) : NaN;
  if (!Number.isNaN(since)) {
    seconds.day += secondsBetween(Math.max(since, now - DAY_MS), now);
    seconds.week += secondsBetween(Math.max(since, now - WEEK_MS), now);
  }
  return seconds;
}

// What the instances worked: each one's own time, so all of them add up.
export interface WorkTotals extends WorkSeconds {
  // How many worked in the last 7 days.
  counted: number;
}

export function workTotals(instances: Iterable<HubInstance>, now: number): WorkTotals {
  const totals: WorkTotals = {day: 0, week: 0, counted: 0};
  for (const instance of instances) {
    const seconds = workSeconds(instance, now);
    totals.day += seconds.day;
    totals.week += seconds.week;
    if (seconds.week > 0) totals.counted++;
  }
  return totals;
}

// formatWork is a short duration: "0m", "<1m", "45m", "2.3h", "31h".
// Rounded down, so a figure grows as it runs.
export function formatWork(seconds: number): string {
  if (seconds <= 0) return "0m";
  if (seconds < 60) return "<1m";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 36_000) return `${Math.floor(seconds / 360) / 10}h`;
  return `${Math.floor(seconds / 3600)}h`;
}

// workTitle says what a work time is, for a tooltip.
export function workTitle(day: number, week: number): string {
  return `Worked ${formatWork(day)} in the last 24 hours, ${formatWork(week)} in the last 7 days (as the hub saw it)`;
}
