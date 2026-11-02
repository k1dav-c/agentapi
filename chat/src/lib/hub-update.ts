// Updating the AgentAPIs from the hub: what an instance's summary says of
// updates through its API (POST /update), what its ⋯ menu, its chip and
// the Versions card offer, and the requests, which go through the hub's
// proxy with the instance's token.

import {currentSummary, olderThanHub, type HubInstance, type InstanceUpdate} from "./hub";

// When an AgentAPI updates: once its agent is idle (the task finished and
// nothing waits for an answer), or now, which interrupts the task.
export type UpdateWhen = "idle" | "now";

// The config file lines the hub suggests for a machine: the first lets the
// hub update its AgentAPIs, the second keeps each one's conversation across
// a restart.
export const ALLOW_UPDATE_LINE = "allow-update: true";
export const STATE_DB_LINE = "state-db: ~/.agentapi/state/{port}.db";

// liveUpdate is the update an instance's summary reports, while the hub
// follows it.
function liveUpdate(instance: HubInstance): InstanceUpdate | undefined {
  if (instance.connection !== "connected" || !currentSummary(instance)) return undefined;
  return instance.summary?.update ?? undefined;
}

// UpdateAction is what an instance's ⋯ menu offers for its update.
export type UpdateAction =
  // The update; failed says why the last attempt failed.
  | {kind: "update"; failed?: string}
  // One waits for the agent to be idle: it can be cancelled.
  | {kind: "cancel"; update: InstanceUpdate}
  // One is under way: checking, downloading or restarting.
  | {kind: "busy"; update: InstanceUpdate}
  // It can't replace its binary until a one-time setup on its machine:
  // why, and the command to run there.
  | {kind: "blocked"; reason: string; command?: string}
  // Updates through its API are off on its machine.
  | {kind: "off"};

// updateAction is none for an instance the hub doesn't follow now, and for
// an AgentAPI from before updates through the API: the Versions card says
// how to update those by hand.
export function updateAction(instance: HubInstance): UpdateAction | undefined {
  const update = liveUpdate(instance);
  if (!update) return undefined;
  if (!update.allowed) return {kind: "off"};
  if (update.blocked) return {kind: "blocked", ...setupCommand(update.blocked)};
  switch (update.state) {
    case "idle":
      return {kind: "update"};
    case "failed":
      return {kind: "update", failed: update.error || "It didn't say why."};
    case "waiting":
      return {kind: "cancel", update};
    default:
      return {kind: "busy", update};
  }
}

// setupCommand splits blocked, which says why an AgentAPI can't replace its
// binary and how to fix that once, into the reason and the command to run:
// the one in backquotes, else the text from its mkdir -p to the end of the
// sentence.
export function setupCommand(blocked: string): {reason: string; command?: string} {
  const text = blocked.trim();
  for (const quoted of text.matchAll(/`([^`]+)`/g)) {
    if (/mkdir|ln -s|&&/.test(quoted[1])) return split(text, quoted.index, quoted.index + quoted[0].length, quoted[1]);
  }
  const start = text.search(/\bmkdir -p\s/);
  if (start < 0) return {reason: text};
  const end = text.slice(start).search(/\.(\s+[A-Z]|\s*$)/);
  const stop = end < 0 ? text.length : start + end;
  return split(text, start, stop, text.slice(start, stop));
}

// split takes the command out of text; a short lead-in that ends in a colon
// ("once:", "Run once:") goes with it.
function split(text: string, start: number, stop: number, command: string): {reason: string; command: string} {
  const before = text
    .slice(0, start)
    .replace(/(^|([.;])\s+)[^.;:]{0,30}:\s*$/, "$2")
    .replace(/\s+/g, " ")
    .replace(/[\s:;,–—-]+$/, "");
  const after = text.slice(stop).replace(/^[\s.]+/, "").trim();
  const reason = before && after ? `${before}${/[.!?]$/.test(before) ? "" : "."} ${after}` : before || after;
  return {reason, command: command.trim()};
}

// shownUpdate is the update an instance's chip shows: one under way or one
// that failed, of the run the hub follows. A restarting one stays while the
// hub reconnects; the new run has none under way.
export function shownUpdate(instance: HubInstance): InstanceUpdate | undefined {
  const update = instance.summary?.update;
  if (!update || update.state === "idle" || !currentSummary(instance)) return undefined;
  if (instance.connection === "connected") return update;
  return instance.connection === "connecting" && update.state === "restarting" ? update : undefined;
}

// UpdateBadge is what an update's chip says: a label, a shorter one for a
// phone's bar, the whole of it for a tooltip and screen readers, and its
// tone.
export interface UpdateBadge {
  label: string;
  short: string;
  title: string;
  tone: "working" | "waiting" | "fault";
}

export function updateBadge(update: InstanceUpdate): UpdateBadge {
  const target = update.target ? `v${update.target}` : "";
  switch (update.state) {
    case "checking":
      return {label: "Checking for update", short: "Updating", title: "Updating AgentAPI: checking for the latest release", tone: "working"};
    case "downloading":
      return {
        label: target ? `Downloading ${target}` : "Downloading update",
        short: "Updating",
        title: `Updating AgentAPI: downloading ${target || "the latest release"}`,
        tone: "working",
      };
    case "waiting":
      return {
        label: "Update waits for the task",
        short: "Update waits",
        title: `Updating AgentAPI${target ? ` to ${target}` : ""} once the agent is idle: its task finished, nothing waiting for an answer`,
        tone: "waiting",
      };
    case "restarting":
      return {label: target ? `Restarting into ${target}` : "Restarting", short: "Restarting", title: `Updating AgentAPI: restarting into ${target || "the new release"}`, tone: "working"};
    case "failed":
      return {label: "Update failed", short: "Update failed", title: update.error ? `The AgentAPI update failed: ${update.error}` : "The AgentAPI update failed", tone: "fault"};
    default:
      return {label: "Updating", short: "Updating", title: `Updating AgentAPI: ${update.state}`, tone: "working"};
  }
}

// updateURL is an instance's /update, through the hub's proxy.
export function updateURL(root: string, id: string): string {
  return `${root}/i/${encodeURIComponent(id)}/update`;
}

// updateRequest asks an instance to update; cancelRequest cancels an update
// that waits for the agent to be idle.
export function updateRequest(when: UpdateWhen): RequestInit {
  return {method: "POST", credentials: "same-origin", headers: {"Content-Type": "application/json"}, body: JSON.stringify({when})};
}

export function cancelRequest(): RequestInit {
  return {method: "DELETE", credentials: "same-origin"};
}

export type Fetcher = (url: string, init: RequestInit) => Promise<Response>;

// UpdateAnswer is what an instance answered: the state its update is in
// (upToDate: it runs the latest release already), or why it refused (403:
// updates are off; 409: blocked, or restarting already); status 0 when
// nothing answered.
export type UpdateAnswer = {ok: true; state: string; target?: string; upToDate: boolean} | {ok: false; status: number; error: string};

export function requestUpdate(root: string, id: string, when: UpdateWhen, fetcher: Fetcher = fetch): Promise<UpdateAnswer> {
  return send(fetcher, updateURL(root, id), updateRequest(when));
}

export function cancelUpdate(root: string, id: string, fetcher: Fetcher = fetch): Promise<UpdateAnswer> {
  return send(fetcher, updateURL(root, id), cancelRequest());
}

// An answer's body: the update's state, or huma's error, or the hub's own
// (its proxy, the session).
interface AnswerBody {
  state?: string;
  target?: string;
  up_to_date?: boolean;
  detail?: string;
  title?: string;
  error?: string;
}

async function send(fetcher: Fetcher, url: string, init: RequestInit): Promise<UpdateAnswer> {
  let response: Response;
  try {
    response = await fetcher(url, init);
  } catch {
    return {ok: false, status: 0, error: "The hub didn't answer."};
  }
  const body = (await response.json().catch(() => null)) as AnswerBody | null;
  if (response.ok) return {ok: true, state: body?.state || "idle", target: body?.target || undefined, upToDate: body?.up_to_date === true};
  return {ok: false, status: response.status, error: body?.detail || body?.error || body?.title || `It answered ${response.status}.`};
}

// OlderUpdates sorts the instances older than the hub by what the Versions
// card can do for them.
export interface OlderUpdates {
  // targets can update from here now.
  targets: HubInstance[];
  // updating have an update under way.
  updating: HubInstance[];
  // The others can't: their machine needs a one-time setup (blocked), has
  // updates through the API off, or they run an AgentAPI from before them.
  blocked: HubInstance[];
  off: HubInstance[];
  unsupported: HubInstance[];
}

export function olderUpdates(instances: Iterable<HubInstance>, hubVersion: string | undefined): OlderUpdates {
  const older: OlderUpdates = {targets: [], updating: [], blocked: [], off: [], unsupported: []};
  for (const instance of instances) {
    if (!olderThanHub(instance, hubVersion)) continue;
    const action = updateAction(instance);
    switch (action?.kind) {
      case undefined:
        older.unsupported.push(instance);
        break;
      case "off":
        older.off.push(instance);
        break;
      case "blocked":
        older.blocked.push(instance);
        break;
      case "update":
        older.targets.push(instance);
        break;
      case "cancel":
      case "busy":
        older.updating.push(instance);
        break;
    }
  }
  return older;
}

// UpdateResults is what the instances asked to update answered.
export interface UpdateResults {
  asked: HubInstance[];
  upToDate: HubInstance[];
  refused: {instance: HubInstance; error: string}[];
}

// requestUpdates asks each instance to update once its agent is idle, all
// at once: the AgentAPIs of one machine take turns themselves.
export async function requestUpdates(root: string, instances: HubInstance[], fetcher: Fetcher = fetch): Promise<UpdateResults> {
  const answers = await Promise.all(instances.map((instance) => requestUpdate(root, instance.id, "idle", fetcher)));
  const results: UpdateResults = {asked: [], upToDate: [], refused: []};
  answers.forEach((answer, i) => {
    if (!answer.ok) results.refused.push({instance: instances[i], error: answer.error});
    else if (answer.upToDate) results.upToDate.push(instances[i]);
    else results.asked.push(instances[i]);
  });
  return results;
}

// machines names the hosts of instances, each once, sorted.
function machines(instances: HubInstance[]): string[] {
  return [...new Set(instances.map((instance) => instance.hostname ?? "").filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

// count is n with the verb that goes with it: "1 needs", "2 need".
function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

export interface CantUpdate {
  // reasons: why some can't, each with the machines it names.
  reasons: {text: string; hosts: string[]}[];
  // fix: where to look for the fix, or "".
  fix: string;
}

// cantUpdate says which older instances the hub can't update, and why; no
// reasons when it can update them all. The page names the machines in
// elements of their own.
export function cantUpdate(older: OlderUpdates): CantUpdate {
  const reasons = [
    older.blocked.length > 0 && {text: `${count(older.blocked.length, "needs", "need")} a one-time setup`, hosts: machines(older.blocked)},
    older.off.length > 0 && {text: `${count(older.off.length, "has", "have")} updates from the hub off`, hosts: machines(older.off)},
    older.unsupported.length > 0 && {text: `${count(older.unsupported.length, "runs", "run")} an AgentAPI from before updates from the hub`, hosts: []},
  ].filter((reason) => reason !== false);
  const fix = older.blocked.length + older.off.length > 0 ? "The ⋯ menu of each agent says how to fix its machine." : "";
  return {reasons, fix};
}

// cantUpdateText is cantUpdate as one sentence; "" when the hub can update
// them all.
export function cantUpdateText(older: OlderUpdates): string {
  const {reasons, fix} = cantUpdate(older);
  if (reasons.length === 0) return "";
  const named = reasons.map(({text, hosts}) => (hosts.length > 0 ? `${text} (${hosts.join(", ")})` : text));
  return `Can't update from here: ${named.join("; ")}.${fix ? ` ${fix}` : ""}`;
}

// confirmText asks before the hub asks targets to update.
export function confirmText(targets: HubInstance[]): string {
  const fresh = targets.filter((instance) => instance.summary?.update?.keeps_conversation === false).length;
  const ask = `Update ${count(targets.length, "agent", "agents")} once each one's task ends?`;
  if (fresh === 0) return ask;
  return `${ask} ${count(fresh, "runs", "run")} without a state DB, so ${fresh === 1 ? "its conversation starts" : "their conversations start"} fresh after the restart.`;
}

// resultsText says what the instances asked to update answered.
export function resultsText(results: UpdateResults): string {
  const parts = [
    results.asked.length > 0 && `Asked ${count(results.asked.length, "agent", "agents")} to update; each restarts once its task ends`,
    results.upToDate.length > 0 && `${count(results.upToDate.length, "runs", "run")} the latest release already`,
    results.refused.length > 0 && `${results.refused.length} refused`,
  ].filter(Boolean);
  return parts.length > 0 ? `${parts.join(". ")}.` : "";
}
