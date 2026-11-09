"use client";

import {useState} from "react";
import {ArrowUpRight, Building2, Github, ListChecks, User} from "lucide-react";
import {Button} from "@/components/ui/button";
import type {GitHubStatus} from "@/lib/hub-dispatch";
import {appSettingsURL, installationURL, ownerOrg, reposOf, selectionLabel, type GitHubInstallation} from "@/lib/hub-github";
import type {ReposLoad} from "@/lib/hub-github-store";
import {cn} from "@/lib/utils";

// The hub settings' GitHub App: its card (the app, its owner, and each
// installation with its repositories), the pill that says it is
// connected, the card's skeleton, and a row of the ways to change it.

// How many repositories an installation names before "+N".
const named = 4;

export function ConnectionPill({connected}: {connected: boolean}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium",
        connected ? "bg-state-ready/10 text-state-ready" : "bg-muted text-muted-foreground",
      )}
    >
      <span className={cn("size-1.5 rounded-full", connected ? "bg-state-ready" : "bg-muted-foreground/60")} aria-hidden="true" />
      {connected ? "Connected" : "Not connected"}
    </span>
  );
}

export function AppSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border bg-card" aria-hidden="true">
      <div className="flex items-center gap-3 p-4">
        <span className="size-10 animate-pulse rounded-xl bg-muted" />
        <span className="flex-1 space-y-2">
          <span className="block h-3.5 w-40 animate-pulse rounded bg-muted" />
          <span className="block h-2.5 w-28 animate-pulse rounded bg-muted" />
        </span>
      </div>
      <div className="flex items-center gap-3 border-t px-4 py-3.5">
        <span className="size-8 animate-pulse rounded-full bg-muted" />
        <span className="block h-3 w-48 animate-pulse rounded bg-muted" />
      </div>
    </div>
  );
}

// GitHubLink opens GitHub in a new tab: the hub's page stays, and reads
// what changed when it shows again.
export function GitHubLink({href, className, children, ...props}: React.ComponentProps<"a">) {
  return (
    <a href={href} target="_blank" rel="noreferrer" className={className} {...props}>
      {children}
      <span className="sr-only"> (opens GitHub in a new tab)</span>
    </a>
  );
}

// AppCard is the app, its owner and its installations, each with the
// repositories it is on.
export function AppCard({status, repos}: {status: GitHubStatus; repos: ReposLoad}) {
  const installations = status.installations ?? [];
  const org = ownerOrg(status);
  const ownerKind = status.owner_type === "Organization" ? "organization" : status.owner_type === "User" ? "personal account" : "";
  return (
    <div className="hub-rise overflow-hidden rounded-xl border bg-card">
      <div className="flex items-center gap-3 p-4">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-foreground text-background shadow-sm">
          <Github className="size-[18px]" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <GitHubLink
            href={status.html_url}
            className="group inline-flex max-w-full items-center gap-1 rounded-sm font-mono text-sm font-medium outline-none underline-offset-4 hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/40"
          >
            <span className="truncate">{status.slug}</span>
            <ArrowUpRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-px group-hover:-translate-y-px" aria-hidden="true" />
          </GitHubLink>
          <p className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
            {org ? <Building2 className="size-3 shrink-0" aria-hidden="true" /> : <User className="size-3 shrink-0" aria-hidden="true" />}
            <span className="truncate">
              Owned by <span className="font-medium text-foreground/80">{status.owner}</span>
              {ownerKind && ` · ${ownerKind}`}
            </span>
          </p>
        </div>
      </div>

      {installations.length === 0 ? (
        <div role="status" className="flex flex-wrap items-center gap-3 border-t border-state-needs/25 bg-state-needs/[0.06] px-4 py-3 text-sm">
          <p className="min-w-0 flex-1">
            <span className="font-medium">Installed nowhere yet.</span> <span className="text-muted-foreground">Install it on the repositories its agents work on.</span>
          </p>
          <Button asChild size="sm" className="h-9">
            <GitHubLink href={status.install_url}>
              Install the app <ArrowUpRight />
            </GitHubLink>
          </Button>
        </div>
      ) : (
        <ul aria-label="Installed on" className="divide-y border-t">
          {installations.map((installation) => (
            <InstallationRow key={installation.id} status={status} installation={installation} repos={repos} />
          ))}
        </ul>
      )}

      <div className="flex flex-wrap gap-2 border-t bg-muted/30 px-4 py-3">
        {installations.length > 0 && (
          <Button asChild size="sm" className="h-9">
            <GitHubLink href={status.install_url}>
              <ListChecks /> Choose repositories <ArrowUpRight className="opacity-70" />
            </GitHubLink>
          </Button>
        )}
        <Button asChild variant="outline" size="sm" className="h-9">
          <GitHubLink href={appSettingsURL(status)}>
            App settings on GitHub <ArrowUpRight className="opacity-70" />
          </GitHubLink>
        </Button>
      </div>
    </div>
  );
}

function Avatar({src, name}: {src: string; name: string}) {
  const [failed, setFailed] = useState(false);
  return (
    <span className="relative flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-full bg-muted text-xs font-semibold text-muted-foreground uppercase ring-1 ring-border">
      {name.slice(0, 1)}
      {src && !failed && (
        // GitHub's picture of the account; its first letter while it loads, or if it can't.
        // eslint-disable-next-line @next/next/no-img-element
        <img src={src} alt="" width={32} height={32} loading="lazy" className="absolute inset-0 size-full object-cover" onError={() => setFailed(true)} />
      )}
    </span>
  );
}

function InstallationRow({status, installation, repos}: {status: GitHubStatus; installation: GitHubInstallation; repos: ReposLoad}) {
  const names = repos.state === "ready" ? reposOf(repos.value, installation.account) : undefined;
  const label = selectionLabel(installation, names?.length);
  return (
    <li className="flex items-start gap-3 px-4 py-3">
      <Avatar src={installation.avatar_url} name={installation.account} />
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-baseline gap-x-2">
          <span className="max-w-full truncate text-sm font-medium">{installation.account}</span>
          <span className="text-xs text-muted-foreground tabular-nums">{label}</span>
        </p>
        {repos.state === "loading" && <span className="mt-1.5 block h-5 w-44 animate-pulse rounded-md bg-muted" aria-hidden="true" />}
        {repos.state === "failed" && (
          <p className="mt-1 truncate text-xs text-state-fault" title={repos.error}>
            Couldn&apos;t list its repositories · {repos.error}
          </p>
        )}
        {names && names.length > 0 && (
          <ul aria-label={`Repositories of ${installation.account}`} className="mt-1.5 flex flex-wrap gap-1">
            {names.slice(0, named).map((name) => (
              <li key={name} className="max-w-48 truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                {name}
              </li>
            ))}
            {names.length > named && (
              <li className="rounded-md px-1 py-0.5 text-[11px] text-muted-foreground tabular-nums" title={names.slice(named).join(", ")}>
                +{names.length - named} more
              </li>
            )}
          </ul>
        )}
        {names && names.length === 0 && <p className="mt-1 text-xs text-muted-foreground">No repository yet: choose some on GitHub.</p>}
      </div>
      <Button asChild variant="ghost" size="sm" className="-mr-2 h-8 shrink-0 max-sm:size-8 max-sm:px-0 text-muted-foreground hover:text-foreground">
        <GitHubLink href={installationURL(status, installation)} aria-label={`Choose the repositories of ${installation.account}`}>
          <span className="max-sm:sr-only">Change</span> <ArrowUpRight className="opacity-70" />
        </GitHubLink>
      </Button>
    </li>
  );
}

export function ActionRow({icon, title, body, danger = false, children}: {icon: React.ReactNode; title: string; body: string; danger?: boolean; children: React.ReactNode}) {
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg [&_svg]:size-4",
          danger ? "bg-destructive/10 text-destructive" : "bg-muted text-muted-foreground",
        )}
        aria-hidden="true"
      >
        {icon}
      </span>
      <div className="min-w-0 flex-1 basis-48">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-xs text-pretty text-muted-foreground">{body}</p>
      </div>
      <div className="ml-auto shrink-0">{children}</div>
    </li>
  );
}
