// The hub's GitHub App as its settings show it (GET /api/github): where
// it is installed and on which repositories, and the links to GitHub
// that change it.

import type {GitHubRepo, GitHubStatus} from "./hub-dispatch";

export type GitHubInstallation = NonNullable<GitHubStatus["installations"]>[number];

// connectURL is where the hub registers a GitHub App (lib/dispatch's
// /github/connect): under the signed-in user's account, or org's.
export function connectURL(root: string, org = ""): string {
  const name = org.trim();
  return `${root}/github/connect${name ? `?org=${encodeURIComponent(name)}` : ""}`;
}

// orgName reads an organization's name as GitHub has them: letters,
// digits and single hyphens, not at an end; "" when it isn't one.
export function orgName(input: string): string {
  const name = input.trim().replace(/^@/, "");
  return /^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9])){0,38}$/.test(name) ? name : "";
}

// ownerOrg is the organization the app belongs to, for a new app in the
// same place: "" for a user's, or an owner the hub doesn't know the kind
// of.
export function ownerOrg(status: GitHubStatus): string {
  return status.owner_type === "Organization" ? (status.owner ?? "") : "";
}

// appSettingsURL is the app's settings on GitHub, or its public page while
// the hub doesn't know where its owner keeps them.
export function appSettingsURL(status: GitHubStatus): string {
  return status.settings_url || status.html_url || "";
}

// installationURL is where an installation's account picks its
// repositories: the installation's page, or the app's install page, which
// leads to it.
export function installationURL(status: GitHubStatus, installation: GitHubInstallation): string {
  return installation.settings_url || status.install_url || "";
}

// reposOf are the repositories of repos the installation on account
// reaches: those account owns.
export function reposOf(repos: readonly GitHubRepo[], account: string): string[] {
  const owner = account.toLowerCase();
  return repos.filter((repo) => repo.full_name.split("/")[0].toLowerCase() === owner).map((repo) => repo.full_name.slice(repo.full_name.indexOf("/") + 1));
}

// selectionLabel says which repositories an installation is on: all of
// its account's, or those picked; with how many when the hub listed them
// (undefined: not yet, or it couldn't).
export function selectionLabel(installation: GitHubInstallation, count: number | undefined): string {
  if (installation.repository_selection === "all") return count === undefined ? "All repositories" : `All repositories · ${count}`;
  if (count === undefined) return "Selected repositories";
  return `${count} selected ${count === 1 ? "repository" : "repositories"}`;
}
