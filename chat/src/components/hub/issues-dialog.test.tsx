import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {DispatchHost, DispatchJob} from "@/lib/hub-dispatch";
import {GitHubSection, IssueItem, JobSummary, MachinesLine, ManualStart, type IssueActions} from "./issues-dialog";

const text = (html: string) => html.replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replace(/\s+/g, " ");

const pve: DispatchHost = {
  hostname: "pve-dev",
  url: "http://pve:3291",
  agentapi_version: "3.9.0",
  agents: ["claude", "codex"],
  default_agent: "claude",
  repos: ["acme/widget"],
  jobs: [],
  max_jobs: 4,
  active: 0,
  clone: true,
  online: true,
  credentials_available: false,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "2026-11-08T10:00:00Z",
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
  created_at: "2026-11-08T10:00:00Z",
  updated_at: "2026-11-08T10:00:00Z",
  round: 1,
  runs: [
    {round: 1, agent: "claude", host: "pve-dev", instance_id: "x", started_at: "", cost: {usd: 1.5, calls: 3, tasks: 2, work_seconds: 600, tokens: {input: 1, output: 2, cache_read: 3, cache_write: 4}}},
    {round: 1, agent: "codex", host: "pve-dev", instance_id: "y", started_at: "", cost: {usd: 0.25, calls: 1, tasks: 1, work_seconds: 60, tokens: {input: 1, output: 2, cache_read: 3, cache_write: 4}}},
  ],
};

const actions: IssueActions = {start: async () => true, send: async () => true, stop: async () => true, open: () => {}};
const issue = {number: 12, title: "Fix it", html_url: "https://github.com/acme/widget/issues/12", labels: [{name: "bug"}]};

describe("IssuesDialog's parts", () => {
  test("a job: its state, agent and machine, pull request, spend and chat", () => {
    const shown = text(renderToStaticMarkup(<JobSummary job={job} onOpen={() => {}} />));
    expect(shown).toContain("In review Claude Code on pve-dev");
    expect(shown).toContain("PR #34");
    expect(shown).toContain("$1.75");
    expect(shown).toContain("2 runs");
    expect(shown).toContain("Open chat");
    expect(text(renderToStaticMarkup(<JobSummary job={{...job, state: "done"}} onOpen={() => {}} />))).not.toContain("Open chat");
  });

  test("an issue with work going on: message, another agent, stop", () => {
    const shown = text(renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} job={job} hosts={[pve]} actions={actions} busy={false} />));
    expect(shown).toContain("#12 Fix it bug");
    expect(shown).toContain("Send");
    // The other agents only, picked before Switch.
    expect(shown).toContain("Codex Switch");
    expect(shown).not.toContain("Claude Code Switch");
    expect(shown).toContain("Stop");
    expect(shown).not.toContain("Assign");
    expect(shown).not.toContain("Retry");
  });

  test("Stop and Switch ask first", () => {
    const stop = text(renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} job={job} hosts={[pve]} actions={actions} busy={false} initialConfirm={{kind: "stop"}} />));
    expect(stop).toContain("Stop Claude Code? Its worktree and the branch agent/issue-12 are deleted, here and on GitHub.");
    expect(stop).toContain("Cancel Stop");
    expect(stop).not.toContain("Send");
    const change = text(
      renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} job={job} hosts={[pve]} actions={actions} busy={false} initialConfirm={{kind: "switch", agent: "codex"}} />),
    );
    expect(change).toContain("Stop Claude Code and have Codex take over on the same branch?");
    expect(change).toContain("Switch to Codex");
  });

  test("a failed job: retry with its agent, no message", () => {
    const shown = text(renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} job={{...job, state: "failed", error: "its AgentAPI exited"}} hosts={[pve]} actions={actions} busy={false} />));
    expect(shown).toContain("Retry with Claude Code");
    expect(shown).toContain("its AgentAPI exited");
    expect(shown).not.toContain("Send");
    expect(shown).not.toContain("Open chat");
    expect(shown).toContain("Stop");
  });

  test("an issue without work: the agents and machines that can take it", () => {
    const shown = text(renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} hosts={[pve]} actions={actions} busy={false} />));
    expect(shown).toContain("Machine's default Claude Code Codex");
    expect(shown).toContain("Any machine pve-dev");
    expect(shown).toContain("Assign");
    const none = text(renderToStaticMarkup(<IssueItem repo="acme/widget" issue={issue} hosts={[{...pve, online: false}]} actions={actions} busy={false} />));
    expect(none).toContain("No machine online runs it with room.");
  });

  test("GitHub: connect, or the app and where it is installed", () => {
    const connect = renderToStaticMarkup(<GitHubSection root="http://hub" status={{connected: false, installations: []}} />);
    expect(connect).toContain('href="http://hub/github/connect"');
    expect(text(connect)).toContain("Without it, work starts with each machine's own GitHub login.");
    const connected = text(
      renderToStaticMarkup(
        <GitHubSection
          root=""
          status={{connected: true, slug: "agentapi-hub-x", install_url: "https://github.com/apps/agentapi-hub-x/installations/new", installations: [{id: 7, account: "acme", repository_selection: "selected"}]}}
        />,
      ),
    );
    expect(connected).toContain("GitHub App agentapi-hub-x on acme.");
    expect(connected).toContain("Choose repositories");
  });

  test("the machines, and none", () => {
    expect(text(renderToStaticMarkup(<MachinesLine hosts={[pve, {...pve, hostname: "old", online: false}]} />))).toContain("pve-dev · 0/4 jobs · Claude Code, Codex old · offline");
    expect(text(renderToStaticMarkup(<MachinesLine hosts={[]} />))).toContain("run agentapi host install");
  });

  test("a hub without GitHub starts an issue by its reference", () => {
    const shown = text(renderToStaticMarkup(<ManualStart hosts={[pve]} busy={false} onStart={async () => true} />));
    expect(shown).toContain("Start an issue");
    expect(shown).toContain("Machine's default Claude Code Codex");
  });
});
