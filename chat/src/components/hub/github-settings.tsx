"use client";

import {useEffect, useState} from "react";
import {ArrowRight, ArrowUpRight, Building2, Github, KeyRound, ListChecks, RefreshCw, RotateCcw, Trash2, TriangleAlert, Unplug, User} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {hubErrorText} from "@/lib/hub-api";
import type {GitHubStatus} from "@/lib/hub-dispatch";
import type {ReposLoad} from "@/lib/hub-github-store";
import {appSettingsURL, connectURL, installationURL, orgName, ownerOrg, reposOf, selectionLabel, type GitHubInstallation} from "@/lib/hub-github";
import {cn} from "@/lib/utils";
import {fieldClass} from "./dialogs";
import {ConnectGitHub} from "./issues/setup";
import type {Load} from "@/lib/load";
import {useHubGitHub} from "./use-hub-github";

// How many repositories an installation names before "+N".
const named = 4;

// GitHubSettings is the GitHub section of the hub's settings: the hub's
// GitHub App, where it is installed and on which repositories, and the
// ways to change that on GitHub, connect another app, or disconnect.
// activeJobs is how many jobs at work push with the app's tokens. What it
// reads, and a disconnect, is the Issues page's too (useHubGitHub).
export function GitHubSettings({root, activeJobs = 0, headingRef}: {root: string; activeJobs?: number; headingRef?: React.Ref<HTMLHeadingElement>}) {
  const github = useHubGitHub(root);
  const shown = github.status.state === "ready";
  return (
    <GitHubSettingsView
      root={root}
      status={github.status}
      repos={github.repos}
      reading={github.reading}
      activeJobs={activeJobs}
      headingRef={headingRef}
      onRetry={async () => {
        // What it read stays: a toast says this read failed.
        const why = await github.read();
        if (why && shown) toast.error("Couldn't read the hub's GitHub setup again", {description: why});
      }}
      onDisconnect={github.disconnect}
    />
  );
}

// GitHubSettingsView draws the section from what was read.
export function GitHubSettingsView({
  root,
  status,
  repos,
  reading,
  activeJobs,
  headingRef,
  onRetry,
  onDisconnect,
}: {
  root: string;
  status: Load<GitHubStatus>;
  repos: ReposLoad;
  reading: boolean;
  activeJobs: number;
  headingRef?: React.Ref<HTMLHeadingElement>;
  onRetry: () => void;
  onDisconnect: () => Promise<void>;
}) {
  const [asking, setAsking] = useState<"reconnect" | "disconnect" | null>(null);
  const app = status.state === "ready" && status.value.connected ? status.value : undefined;
  return (
    <section aria-labelledby="hub-settings-github" aria-busy={status.state === "loading"} className="space-y-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 id="hub-settings-github" ref={headingRef} tabIndex={-1} className="flex items-center gap-2 text-base font-semibold outline-none">
            GitHub
            {status.state === "ready" && <ConnectionPill connected={!!app} />}
          </h3>
          <p className="text-sm text-muted-foreground">The hub&apos;s own GitHub App lists your issues, and its jobs push branches and open pull requests as it.</p>
        </div>
        {status.state !== "loading" && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-9 shrink-0 rounded-full text-muted-foreground hover:text-foreground"
            aria-label="Read the GitHub setup again"
            title="Read again"
            disabled={reading}
            onClick={onRetry}
          >
            <RefreshCw className={cn(reading && "animate-spin")} />
          </Button>
        )}
      </div>

      {status.state === "loading" && <AppSkeleton />}

      {status.state === "failed" && (
        <div role="alert" className="flex flex-wrap items-center gap-3 rounded-xl border border-state-fault/25 bg-state-fault/[0.06] px-3.5 py-3 text-sm">
          <TriangleAlert className="size-4 shrink-0 text-state-fault" aria-hidden="true" />
          <p className="min-w-0 flex-1">
            Couldn&apos;t read the hub&apos;s GitHub setup <span className="text-muted-foreground">· {status.error}</span>
          </p>
          <Button type="button" size="sm" variant="outline" className="h-8" disabled={reading} onClick={onRetry}>
            Try again
          </Button>
        </div>
      )}

      {status.state === "ready" && !app && <ConnectGitHub root={root} compact />}

      {app && (
        <>
          <AppCard status={app} repos={repos} />
          <ul aria-label="Change the app" className="divide-y overflow-hidden rounded-xl border bg-card">
            <ActionRow
              icon={<RotateCcw />}
              title="Connect a new app"
              body="For another account or organization, or a fresh key. This app keeps working until GitHub hands the new one back."
            >
              <Button type="button" variant="outline" size="sm" className="h-9" onClick={() => setAsking("reconnect")}>
                Reconnect…
              </Button>
            </ActionRow>
            <ActionRow icon={<Unplug />} title="Disconnect" body="The hub forgets the app. It stays on GitHub until you delete it there." danger>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-9 text-destructive hover:bg-destructive/10 hover:text-destructive dark:hover:bg-destructive/15"
                onClick={() => setAsking("disconnect")}
              >
                Disconnect…
              </Button>
            </ActionRow>
          </ul>
          <ReconnectDialog root={root} status={app} open={asking === "reconnect"} onClose={() => setAsking(null)} />
          <DisconnectDialog status={app} activeJobs={activeJobs} open={asking === "disconnect"} onClose={() => setAsking(null)} onDisconnect={onDisconnect} />
        </>
      )}
    </section>
  );
}

function ConnectionPill({connected}: {connected: boolean}) {
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

function AppSkeleton() {
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

// ExternalLink opens GitHub in a new tab: the hub's page stays, and reads
// what changed when it shows again.
function ExternalLink({href, className, children, ...props}: React.ComponentProps<"a">) {
  return (
    <a href={href} target="_blank" rel="noreferrer" className={className} {...props}>
      {children}
      <span className="sr-only"> (opens GitHub in a new tab)</span>
    </a>
  );
}

// AppCard is the app, its owner and its installations, each with the
// repositories it is on.
function AppCard({status, repos}: {status: GitHubStatus; repos: ReposLoad}) {
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
          <ExternalLink
            href={status.html_url}
            className="group inline-flex max-w-full items-center gap-1 rounded-sm font-mono text-sm font-medium outline-none underline-offset-4 hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/40"
          >
            <span className="truncate">{status.slug}</span>
            <ArrowUpRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-px group-hover:-translate-y-px" aria-hidden="true" />
          </ExternalLink>
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
            <ExternalLink href={status.install_url}>
              Install the app <ArrowUpRight />
            </ExternalLink>
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
            <ExternalLink href={status.install_url}>
              <ListChecks /> Choose repositories <ArrowUpRight className="opacity-70" />
            </ExternalLink>
          </Button>
        )}
        <Button asChild variant="outline" size="sm" className="h-9">
          <ExternalLink href={appSettingsURL(status)}>
            App settings on GitHub <ArrowUpRight className="opacity-70" />
          </ExternalLink>
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
        <ExternalLink href={installationURL(status, installation)} aria-label={`Choose the repositories of ${installation.account}`}>
          <span className="max-sm:sr-only">Change</span> <ArrowUpRight className="opacity-70" />
        </ExternalLink>
      </Button>
    </li>
  );
}

function ActionRow({icon, title, body, danger = false, children}: {icon: React.ReactNode; title: string; body: string; danger?: boolean; children: React.ReactNode}) {
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

// Note is one line of what a dialog's action does.
function Note({icon, children}: {icon: React.ReactNode; children: React.ReactNode}) {
  return (
    <li className="flex gap-3 px-3.5 py-3 text-sm">
      <span className="mt-0.5 shrink-0 text-muted-foreground [&_svg]:size-4" aria-hidden="true">
        {icon}
      </span>
      <span className="min-w-0 text-pretty">{children}</span>
    </li>
  );
}

// ReconnectDialog connects a new app, for the user's account or an
// organization: the hub's connect page, which goes on to GitHub. The hub
// keeps its app until GitHub hands the new one back, so leaving the flow
// on GitHub changes nothing.
export function ReconnectDialog({root, status, open, onClose}: {root: string; status: GitHubStatus; open: boolean; onClose: () => void}) {
  const prefill = ownerOrg(status);
  const [forOrg, setForOrg] = useState(prefill !== "");
  const [org, setOrg] = useState(prefill || (status.owner_type ? "" : (status.owner ?? "")));
  useEffect(() => {
    if (!open) return;
    setForOrg(prefill !== "");
    setOrg(prefill || (status.owner_type ? "" : (status.owner ?? "")));
  }, [open, prefill, status.owner, status.owner_type]);
  const name = orgName(org);
  const ready = !forOrg || name !== "";
  const href = connectURL(root, forOrg ? name : "");
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Connect a new GitHub App?</DialogTitle>
          <DialogDescription>The hub registers a new app on GitHub, from a manifest, and you install it on your repositories.</DialogDescription>
        </DialogHeader>
        <ul className="divide-y rounded-xl border bg-card">
          <Note icon={<RotateCcw />}>
            <span className="font-mono text-[13px]">{status.slug}</span> keeps working until GitHub hands the new app back. Cancel on GitHub, and nothing changes.
          </Note>
          <Note icon={<KeyRound />}>Until you install the new app, jobs at work can&apos;t get repository tokens, and the issues list is empty.</Note>
          <Note icon={<Trash2 />}>
            <span>
              <span className="font-mono text-[13px]">{status.slug}</span> stays on GitHub.{" "}
              <ExternalLink href={appSettingsURL(status)} className="font-medium underline underline-offset-4 hover:text-foreground">
                Delete it in its settings
              </ExternalLink>{" "}
              once you don&apos;t need it.
            </span>
          </Note>
        </ul>
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">The new app belongs to</legend>
          <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="The new app belongs to">
            <OwnerOption checked={!forOrg} onSelect={() => setForOrg(false)} icon={<User />} title="Your account" body="The one signed in on GitHub" />
            <OwnerOption checked={forOrg} onSelect={() => setForOrg(true)} icon={<Building2 />} title="An organization" body="You own or manage its apps" />
          </div>
          {forOrg && (
            <label className="block space-y-1.5 pt-1">
              <span className="sr-only">Organization</span>
              <input
                autoFocus
                value={org}
                onChange={(event) => setOrg(event.target.value)}
                placeholder="Organization"
                aria-label="Organization"
                aria-invalid={org.trim() !== "" && name === ""}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                className={cn(fieldClass, "font-mono")}
              />
              <span className="block h-4 text-xs text-muted-foreground" aria-live="polite">
                {org.trim() !== "" && name === "" ? "That isn't an organization's name." : ""}
              </span>
            </label>
          )}
        </fieldset>
        <DialogFooter>
          <Button type="button" variant="outline" className="h-10" onClick={onClose}>
            Cancel
          </Button>
          {ready ? (
            <Button asChild className="h-10">
              <a href={href}>
                Continue on GitHub <ArrowRight />
              </a>
            </Button>
          ) : (
            <Button type="button" className="h-10" disabled>
              Continue on GitHub <ArrowRight />
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function OwnerOption({checked, onSelect, icon, title, body}: {checked: boolean; onSelect: () => void; icon: React.ReactNode; title: string; body: string}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      onClick={onSelect}
      className={cn(
        "flex flex-col items-start gap-1 rounded-xl border bg-card p-3 text-left outline-none transition-[border-color,box-shadow,background-color] focus-visible:ring-[3px] focus-visible:ring-ring/40",
        checked ? "border-foreground/70 shadow-xs ring-1 ring-foreground/70" : "hover:bg-accent/60",
      )}
    >
      <span className={cn("[&_svg]:size-4", checked ? "text-foreground" : "text-muted-foreground")} aria-hidden="true">
        {icon}
      </span>
      <span className="text-sm font-medium">{title}</span>
      <span className="text-xs text-muted-foreground">{body}</span>
    </button>
  );
}

// DisconnectDialog asks before the hub forgets its app (DELETE
// /api/github), with what that stops.
export function DisconnectDialog({
  status,
  activeJobs,
  open,
  onClose,
  onDisconnect,
}: {
  status: GitHubStatus;
  activeJobs: number;
  open: boolean;
  onClose: () => void;
  onDisconnect: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) setError("");
  }, [open]);
  const disconnect = async () => {
    setBusy(true);
    setError("");
    try {
      await onDisconnect();
      toast.success(`Disconnected ${status.slug}`, {description: "It is still on GitHub."});
      onClose();
    } catch (e) {
      setError(hubErrorText(e));
    } finally {
      setBusy(false);
    }
  };
  const jobs = activeJobs === 1 ? "The job at work" : activeJobs > 1 ? `The ${activeJobs} jobs at work` : "Jobs at work";
  return (
    <Dialog open={open} onOpenChange={(next) => !next && !busy && onClose()}>
      <DialogContent role="alertdialog" className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            Disconnect <span className="font-mono">{status.slug}</span>?
          </DialogTitle>
          <DialogDescription>The hub forgets its GitHub App: its key, and where it is installed.</DialogDescription>
        </DialogHeader>
        <ul className="divide-y rounded-xl border bg-card">
          <Note icon={<KeyRound />}>
            {jobs} can&apos;t get new repository tokens: their pushes and pull requests fail once the token they have expires, within the hour.
          </Note>
          <Note icon={<ListChecks />}>The issues list is empty until you connect GitHub again.</Note>
          <Note icon={<Github />}>
            <span>
              The app stays on GitHub, installed where it is.{" "}
              <ExternalLink href={appSettingsURL(status)} className="font-medium underline underline-offset-4 hover:text-foreground">
                Delete it in its settings
              </ExternalLink>
              .
            </span>
          </Note>
        </ul>
        {error && (
          <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" /> Couldn&apos;t disconnect: {error}
          </p>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="h-10" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" className="h-10" onClick={() => void disconnect()} disabled={busy}>
            {busy ? <RefreshCw className="animate-spin" /> : <Unplug />} Disconnect
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
