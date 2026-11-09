"use client";

import {CircleDot, GitPullRequest} from "lucide-react";
import {jobStateLabel, prNumber} from "@/lib/hub-dispatch";
import {cn} from "@/lib/utils";
import {useHubList} from "../list-context";

// JobChip marks a list row whose agent works on an issue: the issue (its
// stage, where the row is named after it, as the launcher names a job's
// AgentAPI) and its pull request, which open the issue in Issues. Nothing
// for an agent of no job.
export function JobChip({instanceID, name, className}: {instanceID: string; name: string; className?: string}) {
  const {jobs, actions} = useHubList();
  const job = jobs.get(instanceID);
  if (!job) return null;
  const pr = prNumber(job.pr_url);
  return (
    <div className={cn("flex min-w-0 basis-full", className)}>
      <button
        type="button"
        onClick={() => actions.openIssue(job.key)}
        aria-label={`Open ${job.key} in Issues: ${jobStateLabel(job.state)}${pr !== undefined ? `, pull request #${pr}` : ""}`}
        className="inline-flex h-6 min-w-0 items-center gap-1.5 rounded-full border bg-background/70 px-2 text-[11px] font-medium text-muted-foreground outline-none transition-colors hover:border-foreground/20 hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <CircleDot className="size-3 shrink-0" aria-hidden="true" />
        {name === job.key ? <span className="min-w-0 truncate">Issue · {jobStateLabel(job.state)}</span> : <span className="min-w-0 truncate font-mono">{job.key}</span>}
        {pr !== undefined && (
          <>
            <span aria-hidden="true" className="text-muted-foreground/40">
              ·
            </span>
            <span className="inline-flex shrink-0 items-center gap-0.5">
              <GitPullRequest className="size-3" aria-hidden="true" />#{pr}
            </span>
          </>
        )}
      </button>
    </div>
  );
}
