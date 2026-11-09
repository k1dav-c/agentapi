"use client";

import {useState} from "react";
import {ChevronDown, FolderGit2} from "lucide-react";
import TextareaAutosize from "react-textarea-autosize";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogTitle} from "@/components/ui/dialog";
import {DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import {agentLabel, hostAgents, hostsFor, type CreateIssueRequest, type DispatchHost} from "@/lib/hub-dispatch";
import {suggest} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";

const chip =
  "inline-flex h-8 items-center gap-1.5 rounded-full border bg-background px-3 text-xs font-medium outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 data-[state=open]:bg-accent dark:bg-input/30";

// NewIssueDialog opens an issue on GitHub as the hub's app, and with
// Assign on, hands it to an agent at once. What was typed stays when that
// fails.
export function NewIssueDialog({
  open,
  onOpenChange,
  repos,
  initialRepo,
  hosts,
  preferred,
  busy,
  onCreate,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  repos: string[];
  initialRepo: string;
  hosts: DispatchHost[];
  preferred: string;
  busy: boolean;
  onCreate: (request: CreateIssueRequest) => Promise<boolean>;
}) {
  const [repo, setRepo] = useState(initialRepo);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [assign, setAssign] = useState(true);
  const [agent, setAgent] = useState<string | null>(null);
  const target = repo || repos[0] || "";
  const suggestion = suggest(hosts, target, preferred);
  const chosen = agent ?? suggestion?.agent ?? "";
  const agents = hostAgents(hosts).filter((a) => hostsFor(hosts, target, a).length > 0);
  const canAssign = agents.length > 0;
  const submit = async () => {
    if (!title.trim() || !target || busy) return;
    if (await onCreate({repo: target, title: title.trim(), body, assign: assign && canAssign ? {agent: chosen} : undefined})) {
      setTitle("");
      setBody("");
      onOpenChange(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) setRepo(initialRepo);
        onOpenChange(next);
      }}
    >
      <DialogContent
        className="gap-0 overflow-hidden rounded-2xl p-0 sm:max-w-xl max-sm:top-auto max-sm:bottom-0 max-sm:translate-y-0 max-sm:rounded-b-none max-sm:pb-[env(safe-area-inset-bottom)] max-sm:data-[state=open]:slide-in-from-bottom-1/2"
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void submit();
          }
        }}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="flex items-center gap-2 border-b px-5 py-3.5 pr-12">
            <DialogTitle className="text-sm font-semibold">New issue</DialogTitle>
            <span className="text-muted-foreground/50" aria-hidden="true">
              in
            </span>
            {repos.length > 1 ? (
              <DropdownMenu>
                <DropdownMenuTrigger className={chip} aria-label={`Repository: ${target}`}>
                  <FolderGit2 className="size-3.5 text-muted-foreground" aria-hidden="true" />
                  <span className="font-mono">{target}</span>
                  <ChevronDown className="size-3.5 text-muted-foreground" aria-hidden="true" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="rounded-xl">
                  {repos.map((name) => (
                    <DropdownMenuItem key={name} onSelect={() => setRepo(name)} className="font-mono text-[13px]">
                      {name}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            ) : (
              <span className="font-mono text-xs">{target}</span>
            )}
          </div>
          <DialogDescription className="sr-only">Opens an issue on GitHub as the hub&apos;s app, and hands it to an agent when Assign is on.</DialogDescription>
          <div className="px-5 pt-4 pb-2">
            <input
              autoFocus
              required
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="Title"
              aria-label="Title"
              className="w-full bg-transparent text-xl font-semibold tracking-tight outline-none placeholder:text-muted-foreground/50"
            />
            <TextareaAutosize
              minRows={5}
              maxRows={14}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              placeholder="What should change, and how to tell it's done"
              aria-label="Description"
              className="mt-3 w-full resize-none bg-transparent text-sm leading-relaxed outline-none placeholder:text-muted-foreground/60"
            />
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t bg-muted/30 px-5 py-3">
            <label className={cn("flex items-center gap-2.5 text-sm", !canAssign && "opacity-50")}>
              <button
                type="button"
                role="switch"
                aria-checked={assign && canAssign}
                disabled={!canAssign}
                onClick={() => setAssign(!assign)}
                className={cn(
                  "relative h-5 w-9 shrink-0 rounded-full outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
                  assign && canAssign ? "bg-primary" : "bg-input",
                )}
              >
                <span className={cn("absolute top-0.5 left-0.5 size-4 rounded-full bg-background shadow transition-transform duration-200", assign && canAssign && "translate-x-4")} />
              </button>
              Assign to
            </label>
            <DropdownMenu>
              <DropdownMenuTrigger className={chip} disabled={!assign || !canAssign} aria-label={`Agent: ${agentLabel(chosen)}`}>
                {chosen && <AgentMark agentType={chosen} className="size-3.5" />}
                {canAssign ? agentLabel(chosen) : "no machine with room"}
                <ChevronDown className="size-3.5 text-muted-foreground" aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="rounded-xl">
                {agents.map((name) => (
                  <DropdownMenuItem key={name} onSelect={() => setAgent(name)} className="gap-2.5">
                    <AgentMark agentType={name} className="size-4" />
                    {agentLabel(name)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            <span className="flex-1" />
            <span className="hidden text-[11px] text-muted-foreground sm:inline">
              <kbd className="font-mono">⌘↵</kbd>
            </span>
            <Button type="submit" size="sm" disabled={busy || !title.trim() || !target}>
              {busy ? "Opening…" : assign && canAssign ? "Open and assign" : "Open issue"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
