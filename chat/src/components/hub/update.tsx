"use client";

import {Fragment, useEffect, useState} from "react";
import {CircleArrowUp, CircleX, Hourglass, LoaderCircle, TriangleAlert} from "lucide-react";
import {toast} from "sonner";
import {CopyCommand} from "@/components/copy-command";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {DropdownMenuItem, DropdownMenuSeparator} from "@/components/ui/dropdown-menu";
import {displayName, instanceState, instanceVersion, versionRelation, type HubInstance} from "@/lib/hub";
import {
  ALLOW_UPDATE_LINE,
  STATE_DB_LINE,
  cancelUpdate,
  cantUpdate,
  confirmText,
  requestUpdate,
  requestUpdates,
  resultsText,
  updateAction,
  updateBadge,
  type OlderUpdates,
  type UpdateAction,
  type UpdateResults,
  type UpdateWhen,
} from "@/lib/hub-update";

// cancelFromHub cancels an instance's update that waits for its agent to be
// idle; the change comes back on the event stream, an error as a toast.
export async function cancelFromHub(root: string, id: string) {
  const answer = await cancelUpdate(root, id);
  if (!answer.ok) toast.error("Couldn't cancel the update", {description: answer.error});
}

function ItemText({title, note}: {title: string; note?: string}) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      {note && <span className="truncate text-xs text-muted-foreground">{note}</span>}
    </span>
  );
}

const notes: Partial<Record<UpdateAction["kind"], string>> = {blocked: "Needs a one-time setup", off: "Off on this machine"};

// UpdateMenuItems are a row's ⋯ menu items for its update: the update (or
// why it can't, in a dialog), Cancel while it waits for the agent, or one
// under way. None for an instance whose summary doesn't say: one the hub
// doesn't follow now, or an AgentAPI from before updates from the hub.
export function UpdateMenuItems({instance, onOpen, onCancel}: {instance: HubInstance; onOpen: () => void; onCancel: () => void}) {
  const action = updateAction(instance);
  if (!action) return null;
  return (
    <>
      <DropdownMenuSeparator />
      {action.kind === "cancel" ? (
        <DropdownMenuItem onSelect={onCancel}>
          <CircleX /> <ItemText title="Cancel the update" note="It waits for the task to finish" />
        </DropdownMenuItem>
      ) : action.kind === "busy" ? (
        <DropdownMenuItem disabled>
          <LoaderCircle className="animate-spin" /> <ItemText title="Updating…" note={updateBadge(action.update).label} />
        </DropdownMenuItem>
      ) : (
        <DropdownMenuItem onSelect={onOpen}>
          <CircleArrowUp /> <ItemText title="Update AgentAPI…" note={action.kind === "update" && action.failed ? "The last attempt failed" : notes[action.kind]} />
        </DropdownMenuItem>
      )}
    </>
  );
}

function Alert({children}: {children: React.ReactNode}) {
  return (
    <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
      <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" /> <span className="min-w-0 [overflow-wrap:anywhere]">{children}</span>
    </p>
  );
}

const Path = ({children}: {children: React.ReactNode}) => <code className="font-mono text-[12.5px] [overflow-wrap:anywhere]">{children}</code>;

// What the agent does now that the update waits for.
function busyWith(instance: HubInstance): string {
  switch (instanceState(instance)) {
    case "working":
      return `${displayName(instance)} is working now. `;
    case "needs-you":
    case "muted":
      return `${displayName(instance)} waits for your answer. `;
    default:
      return "";
  }
}

// UpdateDialogBody is what the update dialog says of an instance, as its
// summary has it now: the confirm step, the setup its machine needs, that
// its machine has updates off, or the update under way.
export function UpdateDialogBody({
  instance,
  hubVersion,
  sending = false,
  error = "",
  onUpdate,
  onCancel,
}: {
  instance: HubInstance;
  hubVersion: string;
  sending?: boolean;
  error?: string;
  onUpdate: (when: UpdateWhen) => void;
  onCancel: () => void;
}) {
  const action = updateAction(instance);
  const name = displayName(instance);
  // The machine, in an element of its own, as the rows name it.
  const host = instance.hostname ? <span data-hub-host>{instance.hostname}</span> : "its machine";
  switch (action?.kind) {
    case "update": {
      const version = instanceVersion(instance);
      const older = versionRelation(version, hubVersion) === "older";
      return (
        <>
          <DialogHeader>
            <DialogTitle>Update {name}?</DialogTitle>
            <DialogDescription>
              It installs the latest AgentAPI release on {host} and restarts into it.
              {version && (older ? ` It runs v${version}; the hub runs v${hubVersion}.` : ` It runs v${version}.`)}
            </DialogDescription>
          </DialogHeader>
          <p className="flex items-start gap-2 text-sm">
            <Hourglass className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>{busyWith(instance)}The update waits for the current task to finish, and for any approval or question to be answered.</span>
          </p>
          {!instance.summary?.update?.keeps_conversation && (
            <div className="rounded-lg border border-state-needs/35 bg-state-needs/6 p-3 text-sm">
              <p className="flex items-start gap-2">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-state-needs" aria-hidden="true" />
                <span>
                  It runs without a state DB, so the conversation starts fresh after the restart. To keep conversations across restarts, add this line to{" "}
                  <Path>~/.agentapi/config.yaml</Path> on {host}:
                </span>
              </p>
              <CopyCommand text={STATE_DB_LINE} />
            </div>
          )}
          {action.failed && <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">The last attempt failed: {action.failed}</p>}
          {error && <Alert>{error}</Alert>}
          <p className="text-xs text-muted-foreground">Update now doesn&apos;t wait: it interrupts the running task.</p>
          <DialogFooter className="gap-2 sm:space-x-0">
            <Button variant="outline" className="h-10" disabled={sending} onClick={() => onUpdate("now")}>
              Update now
            </Button>
            <Button className="h-10" data-update-idle disabled={sending} onClick={() => onUpdate("idle")}>
              {sending ? <LoaderCircle className="animate-spin" /> : <CircleArrowUp />} Update when idle
            </Button>
          </DialogFooter>
        </>
      );
    }
    case "blocked":
      return (
        <>
          <DialogHeader>
            <DialogTitle>{instance.hostname ? <span data-hub-host>{instance.hostname}</span> : "Its machine"} needs a one-time setup</DialogTitle>
            <DialogDescription className="[overflow-wrap:anywhere]">{action.reason || "This AgentAPI can't replace its binary."}</DialogDescription>
          </DialogHeader>
          {action.command && (
            <div className="text-sm">
              <p>Run this on {host} once. It needs sudo, which the hub never uses:</p>
              <CopyCommand text={action.command} wrap />
              <p className="mt-3 text-muted-foreground">Then update {name} from here.</p>
            </div>
          )}
        </>
      );
    case "off":
      return (
        <>
          <DialogHeader>
            <DialogTitle>Updates from the hub are off on {host}</DialogTitle>
            <DialogDescription>An AgentAPI takes updates from the hub only on a machine that allows them.</DialogDescription>
          </DialogHeader>
          <div className="text-sm">
            <p>
              To allow them, add this line to <Path>~/.agentapi/config.yaml</Path> on {host}, or run <Path>agentapi hub join</Path> again with{" "}
              <Path>--allow-update</Path>:
            </p>
            <CopyCommand text={ALLOW_UPDATE_LINE} />
            <p className="mt-3 text-muted-foreground">
              Its AgentAPIs read it when they start: restart them once with <Path>agentapi restart --all</Path>.
            </p>
          </div>
        </>
      );
    case "cancel":
    case "busy":
      return (
        <>
          <DialogHeader>
            <DialogTitle>Updating {name}</DialogTitle>
            <DialogDescription>{updateBadge(action.update).title}.</DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          {action.kind === "cancel" && (
            <DialogFooter>
              <Button variant="outline" className="h-10" onClick={onCancel}>
                <CircleX /> Cancel the update
              </Button>
            </DialogFooter>
          )}
        </>
      );
    default:
      return (
        <DialogHeader>
          <DialogTitle>Update {name}</DialogTitle>
          <DialogDescription>The hub isn&apos;t connected to it right now.</DialogDescription>
        </DialogHeader>
      );
  }
}

// UpdateDialog confirms an instance's update and sends it, or shows why the
// instance can't update and what to run on its machine.
export function UpdateDialog({
  instance,
  hubVersion,
  root,
  open,
  onOpenChange,
}: {
  instance: HubInstance;
  hubVersion: string;
  root: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) setError("");
  }, [open]);
  const update = async (when: UpdateWhen) => {
    setSending(true);
    setError("");
    const answer = await requestUpdate(root, instance.id, when);
    setSending(false);
    if (!answer.ok) {
      setError(answer.error);
      return;
    }
    // The chip in its row shows the update from here on.
    onOpenChange(false);
    if (answer.upToDate) toast.info(`${displayName(instance)} runs the latest release already`);
  };
  const cancel = async () => {
    const answer = await cancelUpdate(root, instance.id);
    if (answer.ok) onOpenChange(false);
    else setError(answer.error);
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-lg"
        // The focus starts on Update when idle, not on Update now: Enter
        // shouldn't interrupt a task.
        onOpenAutoFocus={(event) => {
          const idle = (event.currentTarget as HTMLElement).querySelector<HTMLElement>("[data-update-idle]");
          if (!idle) return;
          event.preventDefault();
          idle.focus();
        }}
      >
        <UpdateDialogBody
          instance={instance}
          hubVersion={hubVersion}
          sending={sending}
          error={error}
          onUpdate={(when) => void update(when)}
          onCancel={() => void cancel()}
        />
      </DialogContent>
    </Dialog>
  );
}

// UpdateOlder is the Versions card's part for the agents older than the
// hub: a button that asks those it can update to update once each one's
// task ends, after a confirm step, then what they answered; and which it
// can't update, and why.
export function UpdateOlder({root, older}: {root: string; older: OlderUpdates}) {
  const [confirming, setConfirming] = useState(false);
  const [sending, setSending] = useState(false);
  const [results, setResults] = useState<UpdateResults>();
  const targets = older.targets;
  const total = targets.length + older.updating.length + older.blocked.length + older.off.length + older.unsupported.length;
  const cant = cantUpdate(older);
  const send = async () => {
    setSending(true);
    setResults(await requestUpdates(root, targets));
    setSending(false);
    setConfirming(false);
  };
  const progress = [
    targets.length > 0 && `${targets.length} of ${total} can update from here; each waits for its task to finish.`,
    older.updating.length > 0 && `${older.updating.length} updating now.`,
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <>
      {confirming && targets.length > 0 ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="text-foreground">{confirmText(targets)}</span>
          <span className="flex gap-2">
            <Button size="sm" autoFocus disabled={sending} onClick={() => void send()}>
              {sending ? <LoaderCircle className="animate-spin" /> : <CircleArrowUp />} Update {targets.length}
            </Button>
            <Button size="sm" variant="ghost" disabled={sending} onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </span>
        </div>
      ) : (
        progress && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {targets.length > 0 && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setResults(undefined);
                  setConfirming(true);
                }}
              >
                <CircleArrowUp /> Update the older ones
              </Button>
            )}
            <span>{progress}</span>
          </div>
        )
      )}
      {results && (
        <div role="status" className="text-foreground">
          <p>{resultsText(results)}</p>
          {results.refused.length > 0 && (
            <ul className="mt-1 space-y-0.5 text-destructive">
              {results.refused.map(({instance, error}) => (
                <li key={instance.id} className="[overflow-wrap:anywhere]">
                  {displayName(instance)}: {error}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {cant.reasons.length > 0 && (
        <p>
          Can&apos;t update from here:{" "}
          {cant.reasons.map(({text, hosts}, i) => (
            <Fragment key={text}>
              {i > 0 && "; "}
              {text}
              {hosts.length > 0 && (
                <>
                  {" ("}
                  {hosts.map((host, j) => (
                    <Fragment key={host}>
                      {j > 0 && ", "}
                      <span data-hub-host>{host}</span>
                    </Fragment>
                  ))}
                  {")"}
                </>
              )}
            </Fragment>
          ))}
          .{cant.fix && ` ${cant.fix}`}
        </p>
      )}
    </>
  );
}
