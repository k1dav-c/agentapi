"use client";

import {useEffect, useState} from "react";
import {ArrowRight, Building2, Github, KeyRound, ListChecks, RefreshCw, RotateCcw, Trash2, TriangleAlert, Unplug, User} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {hubErrorText} from "@/lib/hub-api";
import type {GitHubStatus} from "@/lib/hub-dispatch";
import {appSettingsURL, connectURL, orgName, ownerOrg} from "@/lib/hub-github";
import {cn} from "@/lib/utils";
import {GitHubLink} from "./github-app-card";
import {fieldClass} from "./styles";

// The hub settings' GitHub dialogs: connecting a new app, and
// disconnecting the one the hub has.

// Note is one line of what a dialog's action does.
export function Note({icon, children}: {icon: React.ReactNode; children: React.ReactNode}) {
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
              <GitHubLink href={appSettingsURL(status)} className="font-medium underline underline-offset-4 hover:text-foreground">
                Delete it in its settings
              </GitHubLink>{" "}
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
              <GitHubLink href={appSettingsURL(status)} className="font-medium underline underline-offset-4 hover:text-foreground">
                Delete it in its settings
              </GitHubLink>
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
