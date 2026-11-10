"use client";

// The overview's Activity card: a heatmap of what the agents did each day
// of the last year, as the hub saw it (GET /api/activity), shaded by the
// work time, the tokens or the spend. A cell's tooltip says all three.

import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from "react";
import {
  activityGrid,
  activityMetrics,
  activityTotal,
  browserTimeZone,
  cellText,
  fetchActivity,
  formatDate,
  formatMetric,
  levelOf,
  levelThresholds,
  metricValue,
  type Activity,
  type ActivityCell,
  type ActivityMetric,
} from "@/lib/hub-activity";
import {HubAPIError} from "@/lib/hub-api";
import {formatTokens, formatUSD} from "@/lib/format";
import {formatWork} from "@/lib/hub-work";
import {useHubSetting} from "@/lib/hub-settings";
import {cn} from "@/lib/utils";
import {FoldHeading, type Fold} from "./overview-cards";

// How often the card reads the activity again while it shows.
const REFRESH_MS = 10 * 60 * 1000;

// The shades of the levels 0 to 4, from the theme: the hue of finished
// work, stronger by level, over a faint neutral for none.
export const levelClass = ["bg-foreground/[0.07]", "bg-state-ready/30", "bg-state-ready/55", "bg-state-ready/80", "bg-state-ready"] as const;

// useActivity reads the activity in the browser's time zone: now, every
// REFRESH_MS, and when the page comes back into view. A time zone the hub
// doesn't know falls back to UTC.
export function useActivity(root: string): {activity?: Activity; error?: string} {
  const [state, setState] = useState<{activity?: Activity; error?: string}>({});
  const load = useCallback(async () => {
    const zone = browserTimeZone();
    try {
      let activity: Activity;
      try {
        activity = await fetchActivity(root, zone);
      } catch (error) {
        if (!(error instanceof HubAPIError && error.status === 400) || zone === "UTC") throw error;
        activity = await fetchActivity(root, "UTC");
      }
      setState({activity});
    } catch (error) {
      setState((last) => ({...last, error: error instanceof Error ? error.message : String(error)}));
    }
  }, [root]);
  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), REFRESH_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") void load();
    };
    window.addEventListener("focus", onVisible);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(timer);
      window.removeEventListener("focus", onVisible);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [load]);
  return state;
}

// The rows a weekday is named on: Monday, Wednesday and Friday, as GitHub.
const weekdayLabels = new Map([
  [1, "Mon"],
  [3, "Wed"],
  [5, "Fri"],
]);

// ActivityCard reads the activity from the hub at root and shows it.
export function ActivityCard({root, fold}: {root: string; fold?: Fold}) {
  const {activity, error} = useActivity(root);
  const [metric, setMetric] = useHubSetting("activityMetric");
  if (!activity) {
    if (!error) return null;
    return (
      <section className="mt-10" aria-labelledby="hub-activity">
        <FoldHeading id="hub-activity" title="Activity" note="Each day of the last year, as the hub saw it" fold={fold} headline="" />
        {!fold?.folded && <p className="mt-3 text-sm text-muted-foreground">Couldn&apos;t read the activity: {error}</p>}
      </section>
    );
  }
  return <ActivityView activity={activity} metric={metric} onMetric={setMetric} fold={fold} />;
}

// ActivityView is the card of activity, shaded by metric.
export function ActivityView({
  activity,
  metric,
  onMetric,
  fold,
}: {
  activity: Activity;
  metric: ActivityMetric;
  onMetric: (metric: ActivityMetric) => void;
  fold?: Fold;
}) {
  const grid = useMemo(() => activityGrid(activity), [activity]);
  const thresholds = useMemo(() => levelThresholds(activity.days.filter((day) => day.date >= activity.since).map((day) => metricValue(day, metric))), [activity, metric]);
  const total = activityTotal(activity, metric);
  const cells = grid.weeks.flat();
  const today = cells.findLastIndex((cell) => !cell.future);
  const [active, setActive] = useState(today);
  const [tip, setTip] = useState<{index: number; left: number; top: number} | undefined>();
  const wrapper = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);

  // The latest weeks in view where the card is narrower than the year.
  useLayoutEffect(() => {
    const element = scroller.current;
    if (element) element.scrollLeft = element.scrollWidth;
  }, [activity, fold?.folded]);

  const show = (index: number, element: HTMLElement) => {
    const box = wrapper.current?.getBoundingClientRect();
    const cell = element.getBoundingClientRect();
    if (!box) return;
    setTip({index, left: cell.left - box.left + cell.width / 2, top: cell.top - box.top});
  };
  const move = (index: number) => {
    const next = Math.max(0, Math.min(today, index));
    setActive(next);
    const element = scroller.current?.querySelector<HTMLElement>(`[data-cell="${next}"]`);
    element?.focus();
    if (element) show(next, element);
  };
  const onKeyDown = (event: React.KeyboardEvent) => {
    const step: Record<string, number> = {ArrowLeft: -7, ArrowRight: 7, ArrowUp: -1, ArrowDown: 1};
    if (event.key in step) move(active + step[event.key]);
    else if (event.key === "Home") move(0);
    else if (event.key === "End") move(today);
    else return;
    event.preventDefault();
  };

  const label = activityMetrics.find((m) => m.key === metric)?.label ?? "";
  const busiest = activity.days.reduce<{date: string; value: number} | undefined>((best, day) => {
    const value = metricValue(day, metric);
    return value > 0 && (!best || value > best.value) ? {date: day.date, value} : best;
  }, undefined);
  const tipCell = tip ? cells[tip.index] : undefined;

  return (
    <section className="mt-10" aria-labelledby="hub-activity">
      <FoldHeading
        id="hub-activity"
        title="Activity"
        note="Each day of the last year, as the hub saw it"
        fold={fold}
        headline={`${formatMetric(total, metric)} in a year`}
      >
        <div role="radiogroup" aria-label="Shade by" className="ml-auto flex rounded-lg border bg-muted/40 p-0.5 text-xs">
          {activityMetrics.map((m) => (
            <button
              key={m.key}
              type="button"
              role="radio"
              aria-checked={metric === m.key}
              onClick={() => onMetric(m.key)}
              className={cn(
                "rounded-md px-2.5 py-1 font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50",
                metric === m.key && "bg-background text-foreground shadow-xs",
              )}
            >
              {m.label}
            </button>
          ))}
        </div>
      </FoldHeading>
      {!fold?.folded && (
        <div id="hub-activity-body" className="mt-3 rounded-2xl border bg-card p-5 shadow-xs">
          <p className="sr-only">
            {label}: {formatMetric(total, metric)} over the last year
            {busiest ? `; the most on ${formatDate(busiest.date)}, ${formatMetric(busiest.value, metric)}` : ""}. Use the arrow keys in the grid to read each day.
          </p>
          <div ref={wrapper} className="relative">
            <div ref={scroller} className="overflow-x-auto pb-1" onScroll={() => setTip(undefined)}>
              <div
                role="grid"
                aria-label={`${label} each day of the last year`}
                onKeyDown={onKeyDown}
                onMouseLeave={() => setTip(undefined)}
                className="grid min-w-[44rem] gap-[3px]"
                style={{gridTemplateColumns: `1.75rem repeat(${grid.weeks.length}, minmax(0, 1fr))`, maxWidth: `${grid.weeks.length * 1.25 + 1.75}rem`}}
              >
                {grid.months.map((month) => (
                  <span
                    key={month.column}
                    aria-hidden="true"
                    className="text-[10px] leading-4 whitespace-nowrap text-muted-foreground"
                    style={{gridColumn: `${month.column + 2} / span 3`, gridRow: 1}}
                  >
                    {month.label}
                  </span>
                ))}
                {/* The weekdays stay in view while the weeks scroll under them. */}
                {Array.from({length: 8}, (_, row) => (
                  <span
                    key={row}
                    aria-hidden="true"
                    className="sticky left-0 z-[1] -mr-[3px] flex items-center bg-card pr-[3px] text-[10px] leading-none text-muted-foreground"
                    style={{gridColumn: 1, gridRow: row + 1}}
                  >
                    {row > 0 && weekdayLabels.get(row - 1)}
                  </span>
                ))}
                {cells.map((cell, index) => (
                  <Cell
                    key={cell.date}
                    cell={cell}
                    index={index}
                    level={cell.before || !cell.day ? undefined : levelOf(metricValue(cell.day, metric), thresholds)}
                    focusable={index === active}
                    onShow={show}
                    onFocus={() => setActive(index)}
                    onHide={() => setTip(undefined)}
                  />
                ))}
              </div>
            </div>
            {tipCell && tip && <Tooltip cell={tipCell} metric={metric} left={tip.left} top={tip.top} width={wrapper.current?.clientWidth ?? 0} />}
          </div>
          <div className="mt-3 flex flex-wrap items-center justify-between gap-x-6 gap-y-2 text-xs text-muted-foreground">
            <span>{activity.since > (activity.days[0]?.date ?? "") ? `Kept since ${formatDate(activity.since)}` : "The last year"}; each agent&apos;s work adds up, each scope&apos;s spend counts once</span>
            <span className="flex items-center gap-1" aria-hidden="true">
              Less
              {levelClass.map((shade) => (
                <span key={shade} className={cn("size-[11px] rounded-[3px]", shade)} />
              ))}
              More
            </span>
          </div>
        </div>
      )}
    </section>
  );
}

// Cell is a day of the heatmap: focusable while it is the grid's active
// cell (one tab stop for the grid), its figures in its label.
function Cell({
  cell,
  index,
  level,
  focusable,
  onShow,
  onFocus,
  onHide,
}: {
  cell: ActivityCell;
  index: number;
  level?: number;
  focusable: boolean;
  onShow: (index: number, element: HTMLElement) => void;
  onFocus: () => void;
  onHide: () => void;
}) {
  const week = Math.floor(index / 7);
  const style = {gridColumn: week + 2, gridRow: (index % 7) + 2};
  if (cell.future) return <span aria-hidden="true" style={style} />;
  return (
    <span
      role="gridcell"
      data-cell={index}
      data-level={level ?? "none"}
      tabIndex={focusable ? 0 : -1}
      aria-label={cellText(cell)}
      onMouseEnter={(event) => onShow(index, event.currentTarget)}
      onFocus={(event) => {
        onFocus();
        onShow(index, event.currentTarget);
      }}
      onBlur={onHide}
      className={cn(
        "aspect-square min-w-[10px] rounded-[3px] outline-none focus-visible:ring-2 focus-visible:ring-ring",
        level === undefined ? "bg-foreground/[0.025]" : levelClass[level],
      )}
      style={style}
    />
  );
}

// Tooltip says a day's three figures, above its cell, the one shaded by in
// bold; kept within the card.
function Tooltip({cell, metric, left, top, width}: {cell: ActivityCell; metric: ActivityMetric; left: number; top: number; width: number}) {
  const rows: [ActivityMetric, string, string][] = cell.day
    ? [
        ["work", "Work time", formatWork(cell.day.work_seconds)],
        ["tokens", "Tokens", formatTokens(cell.day.tokens.total)],
        ["spend", "Spend", formatUSD(cell.day.usd)],
      ]
    : [];
  const half = 88;
  const x = width > 2 * half ? Math.max(half, Math.min(width - half, left)) : left;
  return (
    <div
      role="tooltip"
      className="pointer-events-none absolute z-10 w-44 -translate-x-1/2 -translate-y-full rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
      style={{left: x, top: top - 6}}
    >
      <p className="font-semibold">{formatDate(cell.date)}</p>
      {cell.before || !cell.day ? (
        <p className="mt-1 text-muted-foreground">Before the hub kept its activity</p>
      ) : (
        <dl className="mt-1 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5">
          {rows.map(([key, name, value]) => (
            <div key={key} className={cn("contents", key === metric ? "font-semibold text-foreground" : "text-muted-foreground")}>
              <dt>{name}</dt>
              <dd className="text-right tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}
