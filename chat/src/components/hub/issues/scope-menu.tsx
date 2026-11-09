"use client";

import {useState} from "react";
import {Check, ChevronDown, FolderGit2, Layers, Search} from "lucide-react";
import {DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import {active, type DispatchJob} from "@/lib/hub-dispatch";
import {cn} from "@/lib/utils";

// How many repositories the menu lists before it asks for a search.
const listed = 8;

// ScopeMenu picks the repositories the board shows: the recent ones (the
// first of repos, which the hub lists the latest pushed or changed first),
// or any one, found by its name; each with the work going on in it.
export function ScopeMenu({repos, recent, jobs, scope, onScope}: {repos: string[]; recent: number; jobs: DispatchJob[]; scope: string; onScope: (repo: string) => void}) {
  const [query, setQuery] = useState("");
  const busy = (repo: string) => jobs.filter((job) => active(job) && (repo === "" || job.repo === repo)).length;
  const q = query.trim().toLowerCase();
  const found = q ? repos.filter((repo) => repo.toLowerCase().includes(q)) : repos.slice(0, listed);
  const allLabel = repos.length > recent ? `${recent} most recent repositories` : "All repositories";
  const item = (repo: string, label: string, icon: React.ReactNode) => (
    <DropdownMenuItem key={repo || "all"} onSelect={() => onScope(repo)} className="gap-2.5 rounded-lg py-2">
      {icon}
      <span className={cn("min-w-0 flex-1 truncate", repo && "font-mono text-[13px]")}>{label}</span>
      {busy(repo) > 0 && <span className="text-xs text-state-working tabular-nums">{busy(repo)} active</span>}
      <Check className={cn("size-3.5", scope !== repo && "invisible")} aria-hidden="true" />
    </DropdownMenuItem>
  );
  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger
        className="group inline-flex h-9 max-w-full min-w-0 items-center gap-2 rounded-full border bg-card pr-2.5 pl-3 text-sm font-medium shadow-xs outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 data-[state=open]:bg-accent"
        aria-label={`Repositories: ${scope || allLabel}`}
      >
        {scope ? <FolderGit2 className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" /> : <Layers className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
        <span className={cn("min-w-0 truncate", scope && "font-mono text-[13px]")}>{scope || (repos.length > recent ? "Recent repositories" : "All repositories")}</span>
        <ChevronDown className="size-3.5 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" sideOffset={6} className="w-80 rounded-xl p-1 shadow-lg">
        {item("", allLabel, <Layers className="size-4" aria-hidden="true" />)}
        {repos.length > recent && <p className="px-2 pb-1.5 text-[11px] text-muted-foreground">The issues of those pushed or changed latest; jobs show from all.</p>}
        <DropdownMenuSeparator />
        {repos.length > listed && (
          <div className="relative mx-1 mb-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              // The menu's typeahead would take the letters.
              onKeyDown={(event) => event.key !== "Escape" && event.key !== "ArrowDown" && event.stopPropagation()}
              placeholder={`Find one of ${repos.length}`}
              aria-label="Find a repository"
              className="h-8 w-full rounded-lg border bg-background pr-2 pl-8 text-sm outline-none focus-visible:border-ring"
            />
          </div>
        )}
        {repos.length > listed && !q && <DropdownMenuLabel className="pt-1 text-[11px] font-normal text-muted-foreground">Latest</DropdownMenuLabel>}
        {found.map((repo) => item(repo, repo, <FolderGit2 className="size-4" aria-hidden="true" />))}
        {q && found.length === 0 && <p className="px-2.5 py-2 text-sm text-muted-foreground">No repository matches.</p>}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
