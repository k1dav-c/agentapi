import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {GitHubRepo, GitHubStatus} from "@/lib/hub-dispatch";
import type {ReposLoad} from "@/lib/hub-github-store";
import type {Load} from "@/lib/load";
import {GitHubSettingsView} from "./github-settings";

const text = (html: string) => html.replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replaceAll("&amp;", "&").replace(/\s+/g, " ");
const render = (status: Load<GitHubStatus>, repos: ReposLoad = {state: "none"}) =>
  renderToStaticMarkup(<GitHubSettingsView root="/hub" status={status} repos={repos} reading={false} activeJobs={0} onRetry={() => {}} onDisconnect={async () => {}} />);

const connected: GitHubStatus = {
  connected: true,
  slug: "agentapi-hub-x",
  owner: "acme",
  owner_type: "Organization",
  html_url: "https://github.com/apps/agentapi-hub-x",
  settings_url: "https://github.com/organizations/acme/settings/apps/agentapi-hub-x",
  install_url: "https://github.com/apps/agentapi-hub-x/installations/new",
  installations: [
    {id: 7, account: "acme", account_type: "Organization", repository_selection: "selected", avatar_url: "https://github.com/acme.png?size=96", settings_url: "https://github.com/organizations/acme/settings/installations/7"},
    {id: 8, account: "dave", repository_selection: "all", avatar_url: "https://github.com/dave.png?size=96"},
  ],
};
const repos = (...names: string[]): ReposLoad => ({state: "ready", value: names.map((full_name) => ({full_name}) as GitHubRepo)});

describe("GitHubSettingsView", () => {
  test("connected: the app, where it is installed and on what, and the ways to change it", () => {
    const html = render({state: "ready", value: connected}, repos("acme/widget", "acme/gadget", "acme/a", "acme/b", "acme/c", "dave/dots"));
    const shown = text(html);
    expect(shown).toContain("Connected");
    expect(shown).toContain("Owned by acme · organization");
    expect(html).toContain('href="https://github.com/apps/agentapi-hub-x"');
    // Each installation: its account, picture, which repositories, and its page.
    expect(shown).toContain("acme 5 selected repositories");
    expect(shown).toContain("+1 more");
    expect(shown).toContain("dave All repositories · 1");
    expect(html).toContain('src="https://github.com/acme.png?size=96"');
    expect(html).toContain('href="https://github.com/organizations/acme/settings/installations/7"');
    // dave's installation page isn't known: the install page leads to it.
    expect(html).toMatch(/href="https:\/\/github.com\/apps\/agentapi-hub-x\/installations\/new"[^>]*aria-label="Choose the repositories of dave"/);
    // The actions, opening GitHub in a new tab.
    expect(shown).toContain("Choose repositories");
    expect(html).toMatch(/href="https:\/\/github.com\/organizations\/acme\/settings\/apps\/agentapi-hub-x" target="_blank"/);
    expect(shown).toContain("Reconnect…");
    expect(shown).toContain("Disconnect…");
  });

  test("installed nowhere: the way to install it", () => {
    const shown = text(render({state: "ready", value: {...connected, installations: []}}));
    expect(shown).toContain("Installed nowhere yet.");
    expect(shown).toContain("Install the app");
    expect(shown).not.toContain("Choose repositories");
  });

  test("the repositories while listed, or when the hub couldn't", () => {
    expect(render({state: "ready", value: connected}, {state: "loading"})).toContain("animate-pulse");
    expect(text(render({state: "ready", value: connected}, {state: "failed", error: "GitHub said no"}))).toContain("Couldn't list its repositories · GitHub said no");
    expect(text(render({state: "ready", value: connected}))).toContain("acme Selected repositories");
  });

  test("not connected: the connect card, for an account or an organization", () => {
    const html = render({state: "ready", value: {connected: false, installations: []}});
    expect(text(html)).toContain("Not connected");
    expect(html).toContain('href="/hub/github/connect"');
    expect(text(html)).toContain("For an organization?");
    expect(html).not.toContain("Disconnect");
  });

  test("the hub couldn't read it: why, and Try again", () => {
    const shown = text(render({state: "failed", error: "The hub didn't answer."}));
    expect(shown).toContain("Couldn't read the hub's GitHub setup · The hub didn't answer.");
    expect(shown).toContain("Try again");
  });

  test("while it reads", () => {
    const html = render({state: "loading"});
    expect(html).toContain('aria-busy="true"');
    expect(html).not.toContain("Connected");
  });
});
