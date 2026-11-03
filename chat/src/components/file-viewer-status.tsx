"use client";

import {useEffect, useState, type ComponentType, type ReactNode} from "react";
import {FileQuestion, LoaderCircle, RefreshCw, ShieldAlert, TriangleAlert} from "lucide-react";
import {Button} from "./ui/button";
import {useChatActions} from "./chat-provider";
import {useFileViewer, type FileViewerRequest} from "./file-viewer-context";
import type {FileMatches} from "@/lib/chat-api";
import {ApiError, getErrorMessage} from "@/lib/error-utils";
import {cn} from "@/lib/utils";

// What the file viewer's panes show instead of a file: that it loads, a
// notice, or why it couldn't read it.

export function Loading({children, compact = false}: {children: ReactNode; compact?: boolean}) {
  return (
    <p role="status" className={cn("flex items-center gap-2 text-sm text-muted-foreground", compact ? "py-2" : "justify-center px-6 py-16")}>
      <LoaderCircle className="size-4 shrink-0 motion-safe:animate-spin" />
      {children}
    </p>
  );
}

export function Notice({
  icon: Icon,
  title,
  text,
  detail,
  onRetry,
  compact = false,
  alert = false,
}: {
  icon: ComponentType<{className?: string}>;
  title: string;
  text?: string;
  detail?: string;
  onRetry?: () => void;
  compact?: boolean;
  alert?: boolean;
}) {
  return (
    <div
      role={alert ? "alert" : "status"}
      className={cn("flex flex-col gap-1.5", compact ? "items-start rounded-md border border-dashed px-3 py-3" : "items-center px-6 py-16 text-center")}
    >
      <p className="flex items-center gap-2 text-sm font-medium">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        {title}
      </p>
      {text && <p className="max-w-md text-[13px] text-muted-foreground">{text}</p>}
      {detail && <p className="max-w-md font-mono text-xs text-muted-foreground [overflow-wrap:anywhere]">{detail}</p>}
      {onRetry && (
        <Button type="button" size="sm" variant="outline" className="mt-1.5" onClick={onRetry}>
          <RefreshCw />
          Try again
        </Button>
      )}
    </div>
  );
}

// The error of reading the file or its preview. A file that isn't there is
// looked for, once: the agent may have named it relative to a repository
// below the working directory, or in the wrong case.
export function FileLoadError({error, subject, request, onRetry}: {error: unknown; subject: "file" | "preview"; request: FileViewerRequest; onRetry: () => void}) {
  const notFound = error instanceof ApiError && error.status === 404;
  if (notFound && !request.requestedPath) return <FindFile request={request} error={error} onRetry={onRetry} />;
  return <LoadError error={error} subject={subject} onRetry={onRetry} />;
}

// Looks for a file that wasn't where the agent said: one match opens, more
// are offered, none says so.
function FindFile({request, error, onRetry}: {request: FileViewerRequest; error: unknown; onRetry: () => void}) {
  const {findFile} = useChatActions();
  const viewer = useFileViewer();
  const [found, setFound] = useState<FileMatches | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let active = true;
    findFile(request.path).then(
      (result) => {
        if (!active) return;
        if (result.matches.length === 1 && viewer) {
          viewer.open({...request, path: result.matches[0].relative, requestedPath: request.path, change: undefined});
          return;
        }
        setFound(result);
      },
      () => active && setFailed(true),
    );
    return () => {
      active = false;
    };
  }, [findFile, request, viewer]);

  if (failed || (found && found.matches.length === 0)) {
    return (
      <Notice
        alert
        icon={FileQuestion}
        title="File not found"
        text={
          failed
            ? "It may have been deleted, moved or renamed."
            : found?.complete
              ? "No file in the agent's working directory has this path. It may have been deleted, moved or renamed."
              : "It may have been deleted, moved or renamed. The search for it covered only part of the working directory."
        }
        detail={getErrorMessage(error)}
        onRetry={onRetry}
      />
    );
  }
  if (!found) return <Loading>Looking for the file…</Loading>;
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-12 text-center" role="alert">
      <p className="flex items-center gap-2 text-sm font-medium">
        <FileQuestion className="size-4 shrink-0 text-muted-foreground" />
        <span>
          <span className="font-mono">{request.path}</span> isn&apos;t in the agent&apos;s working directory
        </span>
      </p>
      <p className="text-[13px] text-muted-foreground">These files have the same path below it:</p>
      <ul className="flex w-full max-w-lg flex-col gap-1.5">
        {found.matches.map((match) => (
          <li key={match.relative}>
            <button
              type="button"
              onClick={() => viewer?.open({...request, path: match.relative, requestedPath: request.path, change: undefined})}
              className="w-full rounded-md border px-3 py-2 text-left font-mono text-xs outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring [overflow-wrap:anywhere]"
            >
              {match.relative}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function LoadError({error, subject, onRetry, compact}: {error: unknown; subject: "file" | "changes" | "preview"; onRetry: () => void; compact?: boolean}) {
  const status = error instanceof ApiError ? error.status : undefined;
  const detail = getErrorMessage(error);
  if (status === 403) {
    return (
      <Notice
        alert
        compact={compact}
        icon={ShieldAlert}
        title="Outside the agent's working directory"
        text="The file viewer only opens files in the directory the agent works in."
        detail={detail}
      />
    );
  }
  if (status === 404 && subject !== "changes") {
    return <Notice alert compact={compact} icon={FileQuestion} title="File not found" text="It may have been deleted, moved or renamed." detail={detail} onRetry={onRetry} />;
  }
  return (
    <Notice
      alert
      compact={compact}
      icon={TriangleAlert}
      title={subject === "file" ? "Couldn't show the file" : subject === "preview" ? "Couldn't preview the file" : "Couldn't show the changes"}
      detail={detail}
      // A bad request (a directory, an empty path) stays bad.
      onRetry={status === 400 ? undefined : onRetry}
    />
  );
}
