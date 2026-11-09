import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance} from "@/lib/hub";
import type {DispatchHost, DispatchJob} from "@/lib/hub-dispatch";
import {boardCards, suggest, type Card} from "@/lib/hub-issues";
import {FixedHubClock} from "../clock";
import {failure, IssueCard, type CardActions} from "./issue-card";
import {IssueDetail, type DetailActions} from "./issue-detail";
import {HubListContext} from "../list-context";
import {JobChip} from "./job-chip";
import {Money} from "./money";
import {PipelineRail} from "./pipeline-rail";
import {ConnectGitHub, NoLaunchers, StartByRef} from "./setup";

const text = (html: string) => html.replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replaceAll("&amp;", "&").replace(/\s+/g, " ");
const now = Date.parse("2026-11-09T11:00:00Z");
const render = (node: React.ReactNode) => text(renderToStaticMarkup(<FixedHubClock now={now}>{node}</FixedHubClock>));

const pve: DispatchHost = {
  hostname: "pve-dev",
  url: "http://pve:3291",
  agentapi_version: "3.11.0",
  agents: ["claude", "codex"],
  default_agent: "claude",
  repos: ["acme/widget"],
  jobs: [{key: "acme/widget#12", agent: "claude", branch: "agent/issue-12", default_branch: "main", instance_id: "x", port: 1, run: 1, started_at: "", state: "running", worktree: "/src/widget.worktrees/12"}],
  max_jobs: 4,
  active: 1,
  clone: true,
  online: true,
  credentials_available: false,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "2026-11-09T10:59:00Z",
};

const job: DispatchJob = {
  key: "acme/widget#12",
  repo: "acme/widget",
  issue: 12,
  title: "Fix it",
  host: "pve-dev",
  agent: "claude",
  instance_id: "pve-dev~acme~widget~12",
  state: "review",
  issue_url: "https://github.com/acme/widget/issues/12",
  pr_url: "https://github.com/acme/widget/pull/34",
  credentials: true,
  created_at: "2026-11-09T10:00:00Z",
  updated_at: "2026-11-09T10:54:00Z",
  round: 1,
  runs: [
    {round: 1, agent: "claude", host: "pve-dev", instance_id: "x", started_at: "2026-11-09T10:00:00Z", ended_at: "2026-11-09T10:20:00Z", end_reason: "replaced", cost: {usd: 1.5, calls: 3, tasks: 2, work_seconds: 600, tokens: {input: 1, output: 2, cache_read: 3, cache_write: 4}}},
    {round: 1, agent: "codex", host: "pve-dev", instance_id: "y", started_at: "2026-11-09T10:20:00Z", cost: {usd: 0.25, calls: 1, tasks: 1, work_seconds: 60, tokens: {input: 1, output: 2, cache_read: 3, cache_write: 4}}},
  ],
};

const noop = () => {};
const cardActions: CardActions = {select: noop, assign: noop, retry: noop, openChat: noop};
const detailActions: DetailActions = {
  assign: async () => true,
  retry: async () => true,
  switchTo: async () => true,
  stop: async () => true,
  send: async () => true,
  createIssue: async () => undefined,
  refresh: noop,
  openChat: noop,
  assignCard: noop,
};

const cardOf = (j?: DispatchJob, instances?: Map<string, HubInstance>): Card =>
  boardCards(new Map([["acme/widget", [{number: 12, title: "Fix it", html_url: "", labels: [{name: "bug"}], state: "open", body: "It breaks `on save`.", user: {login: "me"}}]]]), j ? [j] : [], "", instances)[0];

const card = (c: Card, extra: Partial<React.ComponentProps<typeof IssueCard>> = {}) =>
  render(<IssueCard card={c} hosts={[pve]} showRepo={false} selected={false} busy={false} flash={false} actions={cardActions} {...extra} />);

describe("an issue's card", () => {
  test("in review: its agent and machine, pull request, spend, runs and the review", () => {
    const shown = card(cardOf(job));
    expect(shown).toContain("#12 In review");
    expect(shown).toContain("Claude Code · pve-dev · #34 · $1 . 75 · 2 runs");
    expect(shown).toContain("Review #34");
  });

  test("to do: one tap to the suggestion, and the picks beside it", () => {
    const todo = cardOf();
    const html = renderToStaticMarkup(
      <FixedHubClock now={now}>
        <IssueCard card={todo} hosts={[pve]} suggested={suggest([pve], "acme/widget")} showRepo={false} selected={false} busy={false} flash={false} actions={cardActions} />
      </FixedHubClock>,
    );
    expect(html).toContain('aria-label="Assign to Claude Code on pve-dev"');
    expect(html).toContain('aria-label="Choose the agent and the machine"');
    expect(text(html)).toContain("bug");
    // Without a machine, Assign says why.
    expect(renderToStaticMarkup(<IssueCard card={todo} hosts={[]} disabledReason="Every machine is offline" showRepo={false} selected={false} busy={false} flash={false} actions={cardActions} />)).toContain(
      "Assign: Every machine is offline",
    );
  });

  test("failed: why, without the log's path, and Retry", () => {
    const shown = card(cardOf({...job, state: "failed", error: "its AgentAPI exited 5 times in 10m0s; see /x/agentapi.log"}));
    expect(shown).toContain("Its AgentAPI exited 5 times in 10m0s");
    expect(shown).not.toContain("/x/agentapi.log");
    expect(shown).toContain("Retry");
    expect(failure(undefined)).toBe("The job failed");
  });

  test("waiting on you: what its agent asks, and Answer", () => {
    const instance = {id: job.instance_id, state: "needs-you", summary: {attention: "request", request: {kind: "approval", title: "go test ./...", id: "r", created_at: ""}}} as unknown as HubInstance;
    const shown = card(cardOf({...job, state: "working"}, new Map([[job.instance_id, instance]])), {instance});
    expect(shown).toContain("Waits for you");
    expect(shown).toContain("Approval go test ./...");
    expect(shown).toContain("Answer");
  });
});

describe("an issue's detail", () => {
  const detail = (c: Card) =>
    render(<IssueDetail card={c} hosts={[pve]} busy={false} actions={detailActions} onClose={noop} sheet={false} />);

  test("its status, totals, timeline and where it lives", () => {
    const shown = detail(cardOf({...job, messages: [{at: "2026-11-09T10:40:00Z", source: "hub", text: "Also the docs", agent: "codex"}, {at: "2026-11-09T10:30:00Z", source: "review", author: "dave", review_state: "changes_requested", comments: 1, text: "Rename it", agent: "claude"}]}));
    expect(shown).toContain("In review");
    expect(shown).toContain("dave requested changes, 1 comment to Claude Code 10:30 AM Rename it");
    expect(shown).toContain("opened pull request #34");
    expect(shown).toContain("Spend $1 . 75");
    expect(shown).toContain("You to Codex 10:40 AM Also the docs");
    expect(shown).toContain("Run 2 · 10:20 AM · 40m Current run · open the chat");
    expect(shown).toContain("Handed over to Codex");
    expect(shown).toContain("agent/issue-12");
    expect(shown).toContain("/src/widget.worktrees/12");
    expect(shown).toContain("Switch agent");
  });

  test("to do: its description, code spans as code, and who it would go to", () => {
    const html = renderToStaticMarkup(
      <FixedHubClock now={now}>
        <IssueDetail card={cardOf()} hosts={[pve]} busy={false} suggested={suggest([pve], "acme/widget")} actions={detailActions} onClose={noop} sheet />
      </FixedHubClock>,
    );
    expect(html).toContain("<code");
    expect(text(html)).toContain("Goes to Claude Code on pve-dev , which has the repository");
    expect(html).toContain('aria-label="Back to the board"');
  });
});

describe("the view's other parts", () => {
  test("the rail counts each stage and the failures", () => {
    const shown = render(<PipelineRail counts={{todo: 4, working: 3, failed: 1, review: 1, done: 2, waiting: 1}} onJump={noop} />);
    expect(shown).toContain("To do 4");
    // In progress counts the failed, and says them and those that wait.
    expect(shown).toContain("In progress 4 1 waits 1 failed");
    expect(shown).toContain("In review 1");
  });

  test("connecting GitHub, no launchers, and an issue by its reference", () => {
    expect(renderToStaticMarkup(<ConnectGitHub root="/hub" />)).toContain('href="/hub/github/connect"');
    expect(render(<NoLaunchers />)).toContain("agentapi host install --now");
    expect(renderToStaticMarkup(<StartByRef hosts={[pve]} busy={false} onStart={async () => true} />)).toContain("owner/repo#12");
  });
});

describe("amounts and a list row's chip", () => {
  test("an amount keeps its text, with room around its point", () => {
    const html = renderToStaticMarkup(<Money usd={3.19} />);
    expect(html.replace(/<[^>]+>/g, "")).toBe("$3.19");
    expect(html).toContain('<span class="mx-[0.06em]">.</span>');
    expect(renderToStaticMarkup(<Money usd={110} />).replace(/<[^>]+>/g, "")).toBe("$110");
  });

  test("a row whose agent works on an issue names it and its pull request; others nothing", () => {
    const opened: string[] = [];
    const list = (jobs: Map<string, DispatchJob>) => ({
      hubVersion: "",
      root: "",
      groups: [],
      prefix: "",
      jobs,
      actions: {select: noop, forget: noop, group: noop, newGroup: noop, mute: noop, pin: noop, move: noop, openIssue: (key: string) => opened.push(key)},
    });
    const html = renderToStaticMarkup(
      <HubListContext.Provider value={list(new Map([[job.instance_id, job]]))}>
        <JobChip instanceID={job.instance_id} name="api server" />
      </HubListContext.Provider>,
    );
    expect(text(html)).toContain("acme/widget#12 · #34");
    // A row named after the issue says its stage instead.
    const named = renderToStaticMarkup(
      <HubListContext.Provider value={list(new Map([[job.instance_id, job]]))}>
        <JobChip instanceID={job.instance_id} name="acme/widget#12" />
      </HubListContext.Provider>,
    );
    expect(text(named)).toContain("Issue · In review · #34");
    expect(html).toContain('aria-label="Open acme/widget#12 in Issues: In review, pull request #34"');
    expect(
      renderToStaticMarkup(
        <HubListContext.Provider value={list(new Map())}>
          <JobChip instanceID="someone" name="someone" />
        </HubListContext.Provider>,
      ),
    ).toBe("");
  });
});
