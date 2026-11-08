import {versionRelation, versionTitle} from "@/lib/hub-version";
import {cn} from "@/lib/utils";

// OlderMark marks a version older than the hub's.
export function OlderMark({className, ...props}: React.ComponentProps<"span">) {
  return (
    <span className={cn("shrink-0 rounded-full bg-muted px-1.5 py-px font-medium text-foreground/80", className)} {...props}>
      older
    </span>
  );
}

// VersionTag is the AgentAPI version an instance runs, marked when it is
// older than the hub's; dimmed when it is the one an instance the hub
// doesn't follow now ran last.
export function VersionTag({version, hubVersion, lastKnown = false, className}: {version: string; hubVersion?: string; lastKnown?: boolean; className?: string}) {
  const older = !lastKnown && versionRelation(version, hubVersion) === "older";
  const title = versionTitle(version, hubVersion, lastKnown);
  return (
    <span className={cn("flex min-w-0 shrink-0 items-center gap-1 tabular-nums", lastKnown && "opacity-55", className)} title={title}>
      <span className="sr-only">{title}</span>
      <span aria-hidden="true" className="max-w-[7rem] truncate">
        v{version}
      </span>
      {older && <OlderMark aria-hidden="true" />}
    </span>
  );
}
