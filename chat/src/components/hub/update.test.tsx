import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {Dialog} from "@/components/ui/dialog";
import type {HubInstance, InstanceUpdate, Summary} from "@/lib/hub";
import {InstanceRow} from "./instance-row";
import {UpdateDialogBody} from "./update";
import {UpdateChip, UpdateStatus} from "./update-chip";

const update = (overrides: Partial<InstanceUpdate> = {}): InstanceUpdate => ({allowed: true, state: "idle", keeps_conversation: true, ...overrides});

const instance = (given: Partial<InstanceUpdate> | null, overrides: Partial<HubInstance> = {}): HubInstance => ({
  id: "dev-1:3284",
  name: "checkout-api",
  url: "http://dev-1:3284",
  server_id: "s",
  hostname: "dev-1",
  agentapi_version: "2.0.0",
  source: "webhook",
  last_seen: "2026-11-01T00:00:00Z",
  connection: "connected",
  state: "idle",
  summary: {
    instance: {id: "dev-1:3284", server_id: "s", agent_type: "claude", transport: "native", agentapi_version: "2.0.0"},
    status: "stable",
    lifecycle: "ready",
    attention: "none",
    title: "",
    pending_requests: 0,
    queued_messages: 0,
    tasks: 0,
    unread: false,
    seen_at: "2026-11-01T00:00:00Z",
    ...(given ? {update: update(given)} : {}),
  } as Summary,
  ...overrides,
});

// The text of html, without its tags; a machine's name, in an element of
// its own (data-hub-host), reads as part of its sentence.
const text = (html: string) =>
  html
    .replace(/<span data-hub-host(="[^"]*")?>([^<]*)<\/span>/g, "$2")
    .replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replaceAll("&quot;", '"').replaceAll("&amp;", "&").replace(/\s+/g, " ");

const COMMAND = "mkdir -p ~/.local/bin && cp /usr/local/bin/agentapi ~/.local/bin/agentapi && sudo ln -sf ~/.local/bin/agentapi /usr/local/bin/agentapi";

const body = (target: HubInstance, props: {error?: string} = {}) =>
  renderToStaticMarkup(
    <Dialog open>
      <UpdateDialogBody instance={target} hubVersion="2.1.0" onUpdate={() => {}} onCancel={() => {}} {...props} />
    </Dialog>,
  );

describe("UpdateChip", () => {
  test("says the state in short, the whole of it to a tooltip and a screen reader", () => {
    const html = renderToStaticMarkup(<UpdateChip update={update({state: "failed", error: "no network"})} />);
    expect(html).toContain('title="The AgentAPI update failed: no network"');
    expect(html).toContain('<span class="sr-only">The AgentAPI update failed: no network</span>');
    expect(html).toContain(">Update failed</span>");
    expect(html).toContain("text-state-fault");
    expect(html).toContain('data-update-state="failed"');
  });

  test("turns while it works, not while it waits", () => {
    expect(renderToStaticMarkup(<UpdateChip update={update({state: "downloading", target: "2.1.0"})} />)).toContain("animate-spin");
    const waiting = renderToStaticMarkup(<UpdateChip update={update({state: "waiting"})} />);
    expect(waiting).not.toContain("animate-spin");
    expect(waiting).toContain(">Update waits for the task</span>");
  });

  test("the bar's chip, shorter on a phone, can cancel an update only while it waits", () => {
    const cancel = 'aria-label="Cancel the update"';
    const waiting = renderToStaticMarkup(<UpdateStatus update={update({state: "waiting"})} onCancel={() => {}} />);
    expect(waiting).toContain(cancel);
    expect(waiting).toMatch(/class="truncate sm:hidden">Update waits<\/span>/);
    expect(waiting).toMatch(/class="truncate hidden sm:block">Update waits for the task<\/span>/);
    expect(renderToStaticMarkup(<UpdateStatus update={update({state: "restarting"})} onCancel={() => {}} />)).not.toContain(cancel);
  });
});

describe("UpdateDialogBody", () => {
  test("confirms: it waits for the task, or doesn't when updated now", () => {
    const shown = text(body(instance({})));
    expect(shown).toContain("Update checkout-api?");
    expect(shown).toContain("It installs the latest AgentAPI release on dev-1 and restarts into it. It runs v2.0.0; the hub runs v2.1.0.");
    expect(shown).toContain("The update waits for the current task to finish, and for any approval or question to be answered.");
    expect(shown).toContain("Update now doesn't wait: it interrupts the running task.");
    expect(shown).toContain("Update now");
    expect(shown).toContain("Update when idle");
    // A state DB keeps the conversation: nothing to warn about.
    expect(shown).not.toContain("state-db");
  });

  test("warns that the conversation starts fresh without a state DB, with the line that keeps it", () => {
    const shown = text(body(instance({keeps_conversation: false})));
    expect(shown).toContain("It runs without a state DB, so the conversation starts fresh after the restart.");
    expect(shown).toContain("~/.agentapi/config.yaml on dev-1:");
    expect(shown).toContain("state-db: ~/.agentapi/state/{port}.db");
  });

  test("says what the agent does now, the last failure and the instance's refusal", () => {
    const shown = text(body(instance({state: "failed", error: "no network"}, {state: "working"}), {error: "the server is stopping"}));
    expect(shown).toContain("checkout-api is working now. The update waits");
    expect(shown).toContain("The last attempt failed: no network");
    expect(body(instance({}), {error: "the server is stopping"})).toMatch(/role="alert"[^>]*>.*the server is stopping/);
  });

  test("blocked: why, and the command to run once on the machine", () => {
    const shown = text(body(instance({blocked: `can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable; once: ${COMMAND}`})));
    expect(shown).toContain("dev-1 needs a one-time setup");
    expect(shown).toContain("can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable");
    expect(shown).toContain("Run this on dev-1 once. It needs sudo, which the hub never uses:");
    expect(shown).toContain(COMMAND);
    expect(shown).not.toContain("Update when idle");
  });

  test("off: the config line that allows updates from the hub", () => {
    const shown = text(body(instance({allowed: false})));
    expect(shown).toContain("Updates from the hub are off on dev-1");
    expect(shown).toContain("allow-update: true");
    expect(shown).toContain("agentapi hub join again with --allow-update");
    expect(shown).toContain("restart them once with agentapi restart --all");
    expect(shown).not.toContain("Update when idle");
  });

  test("one under way: what it does, and Cancel while it waits", () => {
    const waiting = text(body(instance({state: "waiting", target: "2.1.0"})));
    expect(waiting).toContain("Updating checkout-api");
    expect(waiting).toContain("Cancel the update");
    expect(text(body(instance({state: "downloading", target: "2.1.0"})))).not.toContain("Cancel the update");
  });
});

describe("InstanceRow", () => {
  const row = (target: HubInstance) =>
    renderToStaticMarkup(
      <InstanceRow instance={target} hubVersion="2.1.0" root="" groups={[]} order="groups" selected={false} now={0} onSelect={() => {}} onForget={() => {}} onGroup={() => {}} onNewGroup={() => {}} onMute={() => {}} />,
    );

  test("shows an update under way, and says it to a screen reader", () => {
    const html = row(instance({state: "waiting", target: "2.1.0"}));
    expect(html).toContain(">Update waits for the task</span>");
    expect(html).toContain("Older than the hub. Updating AgentAPI to v2.1.0 once the agent is idle");
  });

  test("no chip while no update is under way, or for an older AgentAPI", () => {
    expect(row(instance({}))).not.toContain("data-update-state");
    expect(row(instance(null))).not.toContain("data-update-state");
  });
});
