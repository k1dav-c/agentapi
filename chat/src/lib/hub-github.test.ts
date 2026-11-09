import {describe, expect, test} from "bun:test";
import type {GitHubRepo, GitHubStatus} from "./hub-dispatch";
import {appSettingsURL, connectURL, installationURL, orgName, ownerOrg, reposOf, selectionLabel, type GitHubInstallation} from "./hub-github";

const status = (extra: Partial<GitHubStatus> = {}): GitHubStatus => ({
  connected: true,
  slug: "agentapi-hub-x",
  owner: "acme",
  html_url: "https://github.com/apps/agentapi-hub-x",
  install_url: "https://github.com/apps/agentapi-hub-x/installations/new",
  installations: [],
  ...extra,
});
const installation = (extra: Partial<GitHubInstallation> = {}): GitHubInstallation => ({
  id: 7,
  account: "acme",
  repository_selection: "selected",
  avatar_url: "https://github.com/acme.png?size=96",
  ...extra,
});
const repo = (full_name: string): GitHubRepo => ({full_name, default_branch: "main", private: false, html_url: `https://github.com/${full_name}`}) as GitHubRepo;

describe("connectURL", () => {
  test("the user's account, or an organization's", () => {
    expect(connectURL("/hub")).toBe("/hub/github/connect");
    expect(connectURL("", "  ")).toBe("/github/connect");
    expect(connectURL("", " acme ")).toBe("/github/connect?org=acme");
  });
});

describe("orgName", () => {
  test("GitHub's names only", () => {
    expect(orgName(" acme-labs ")).toBe("acme-labs");
    expect(orgName("@acme")).toBe("acme");
    for (const bad of ["", "-acme", "acme-", "ac--me", "acme/widget", "a b", "x".repeat(40)]) expect(orgName(bad)).toBe("");
  });
});

describe("the app's links", () => {
  test("its settings, or its page while the owner's kind isn't known", () => {
    expect(appSettingsURL(status({settings_url: "https://github.com/organizations/acme/settings/apps/agentapi-hub-x"}))).toBe(
      "https://github.com/organizations/acme/settings/apps/agentapi-hub-x",
    );
    expect(appSettingsURL(status())).toBe("https://github.com/apps/agentapi-hub-x");
  });

  test("an installation's page, or the install page that leads to it", () => {
    expect(installationURL(status(), installation({settings_url: "https://github.com/settings/installations/7"}))).toBe("https://github.com/settings/installations/7");
    expect(installationURL(status(), installation())).toBe("https://github.com/apps/agentapi-hub-x/installations/new");
  });

  test("a new app goes where this one is: an organization's only when the hub knows it is one", () => {
    expect(ownerOrg(status({owner_type: "Organization"}))).toBe("acme");
    expect(ownerOrg(status({owner_type: "User"}))).toBe("");
    expect(ownerOrg(status())).toBe("");
  });
});

describe("an installation's repositories", () => {
  test("those its account owns, by name", () => {
    expect(reposOf([repo("acme/widget"), repo("dave/dots"), repo("Acme/gadget")], "ACME")).toEqual(["widget", "gadget"]);
    expect(reposOf([], "acme")).toEqual([]);
  });

  test("all, or the selected ones, with how many when listed", () => {
    expect(selectionLabel(installation({repository_selection: "all"}), undefined)).toBe("All repositories");
    expect(selectionLabel(installation({repository_selection: "all"}), 12)).toBe("All repositories · 12");
    expect(selectionLabel(installation(), undefined)).toBe("Selected repositories");
    expect(selectionLabel(installation(), 1)).toBe("1 selected repository");
    expect(selectionLabel(installation(), 3)).toBe("3 selected repositories");
  });
});
