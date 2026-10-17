"use client";

import {Fragment, useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode} from "react";
import {FileDiff, FileQuestion, FileText, GitCompare, LoaderCircle, RefreshCw, ShieldAlert, TriangleAlert, X} from "lucide-react";
import {Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle} from "./ui/dialog";
import {Tabs, TabsContent, TabsList, TabsTrigger} from "./ui/tabs";
import {Button} from "./ui/button";
import {useChat} from "./chat-provider";
import {CopyButton} from "./message-list/message-item";
import {DiffView} from "./diff-view";
import {PathLabel} from "./file-path";
import type {FileViewerRequest, FileViewerTab} from "./file-viewer-context";
import type {FileContent, FileDiff as FileChanges, FileDiffStatus} from "@/lib/chat-api";
import {highlightCode, languageForPath, type CodeToken} from "@/lib/code-highlight";
import {ApiError, getErrorMessage} from "@/lib/error-utils";
import {cn} from "@/lib/utils";

// The file viewer: a file the agent works on, with its contents (File) and
// its changes (Diff): the change of the tool call it was opened from, and
// the uncommitted changes git sees. Wide on desktops, full screen on phones.
export function FileViewerDialog({
  request,
  open,
  onOpenChange,
}: {
  request: FileViewerRequest & {id: number};
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        // The header has its own close button.
        className="flex h-dvh max-h-dvh w-full max-w-none flex-col gap-0 overflow-hidden rounded-none border-0 p-0 outline-none sm:h-[min(88dvh,64rem)] sm:w-[min(72rem,calc(100vw-3rem))] sm:max-w-none sm:rounded-lg sm:border [&>[data-slot=dialog-close]]:hidden"
        onOpenAutoFocus={(event) => {
          // Focus the dialog rather than its first button, whose tooltip
          // would open; Tab moves on from there.
          event.preventDefault();
          (event.currentTarget as HTMLElement | null)?.focus();
        }}
        onKeyDown={(event) => {
          // ⌘F / Ctrl+F finds text in the file with the browser's search,
          // not in the tasks behind the dialog.
          if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f") event.stopPropagation();
        }}
      >
        <FileViewerBody key={request.id} request={request} />
      </DialogContent>
    </Dialog>
  );
}

// A tab's panel scrolls by itself; it takes the focus for keyboard
// scrolling, so it shows it.
const panelClass =
  "min-h-0 overflow-auto overscroll-contain pb-[env(safe-area-inset-bottom)] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring";

type Loaded<T> = {state: "loading"} | {state: "ready"; data: T} | {state: "error"; error: unknown};

// A resource read when load is called (again: read again). A response that
// comes after a newer load started is dropped.
function useLoaded<T>(read: () => Promise<T>) {
  const [loaded, setLoaded] = useState<Loaded<T> | null>(null);
  const latest = useRef(0);
  const load = useCallback(() => {
    const id = ++latest.current;
    setLoaded({state: "loading"});
    read().then(
      (data) => {
        if (latest.current === id) setLoaded({state: "ready", data});
      },
      (error: unknown) => {
        if (latest.current === id) setLoaded({state: "error", error});
      },
    );
  }, [read]);
  return [loaded, load] as const;
}

// The dialog's content for one request. A new request remounts it, so every
// open reads the file again.
function FileViewerBody({request}: {request: FileViewerRequest}) {
  const {getFileContent, getFileDiff} = useChat();
  const {path, line, change} = request;
  const [tab, setTab] = useState<FileViewerTab>(request.tab ?? (change ? "diff" : "file"));
  const [file, loadFile] = useLoaded(useCallback(() => getFileContent(path), [getFileContent, path]));
  const [changes, loadChanges] = useLoaded(useCallback(() => getFileDiff(path), [getFileDiff, path]));

  // Each tab reads its part when it is first shown.
  useEffect(() => {
    if (tab === "file" && file === null) loadFile();
  }, [tab, file, loadFile]);
  useEffect(() => {
    if (tab === "diff" && changes === null) loadChanges();
  }, [tab, changes, loadChanges]);

  // The server's path relative to the agent's working directory, once it
  // answered.
  const shownPath =
    (file?.state === "ready" && file.data.relative) || (changes?.state === "ready" && changes.data.relative) || path;
  const current = tab === "file" ? file : changes;
  const reload = tab === "file" ? loadFile : loadChanges;
  const loading = current?.state === "loading";

  return (
    <>
      <header className="flex shrink-0 items-center gap-1 border-b pb-1.5 pl-4 pr-2 pt-[calc(env(safe-area-inset-top)+0.375rem)] sm:pt-1.5">
        <FileText aria-hidden="true" className="mr-1 size-4 shrink-0 text-muted-foreground" />
        <DialogTitle className="min-w-0 flex-1 text-[13px] font-medium leading-5 tracking-normal" title={shownPath}>
          <PathLabel path={shownPath} />
        </DialogTitle>
        <DialogDescription className="sr-only">
          The file&apos;s contents and changes in the agent&apos;s working directory.
        </DialogDescription>
        <CopyButton content={shownPath} label="path" />
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="size-9 text-muted-foreground sm:size-8"
          onClick={reload}
          disabled={loading}
          aria-label={tab === "file" ? "Reload the file" : "Reload the changes"}
          title="Reload"
        >
          <RefreshCw className={cn("size-4", loading && "motion-safe:animate-spin")} />
        </Button>
        <DialogClose asChild>
          <Button type="button" size="icon" variant="ghost" className="size-9 text-muted-foreground sm:size-8" aria-label="Close" title="Close (Esc)">
            <X className="size-4" />
          </Button>
        </DialogClose>
      </header>
      <Tabs value={tab} onValueChange={(value) => setTab(value as FileViewerTab)} className="min-h-0 flex-1 gap-0">
        <div className="shrink-0 border-b px-3 py-2 sm:px-4">
          <TabsList className="grid w-full grid-cols-2 sm:inline-flex sm:w-auto">
            <TabsTrigger value="file" className="sm:px-4">
              <FileText />
              File
            </TabsTrigger>
            <TabsTrigger value="diff" className="sm:px-4">
              <FileDiff />
              Diff
            </TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="file" className={panelClass}>
          <FilePane loaded={file} path={path} line={line} onRetry={loadFile} />
        </TabsContent>
        <TabsContent value="diff" className={panelClass}>
          <DiffPane loaded={changes} change={change} onRetry={loadChanges} />
        </TabsContent>
      </Tabs>
    </>
  );
}

function FilePane({loaded, path, line, onRetry}: {loaded: Loaded<FileContent> | null; path: string; line?: number; onRetry: () => void}) {
  if (!loaded || loaded.state === "loading") return <Loading>Loading the file…</Loading>;
  if (loaded.state === "error") return <LoadError error={loaded.error} subject="file" onRetry={onRetry} />;
  const file = loaded.data;
  if (file.binary) return <Notice icon={FileQuestion} title={`Binary file, ${formatBytes(file.size)}`} text="It isn't text, so it can't be shown here." />;
  if (!file.content) return <Notice icon={FileText} title={file.size === 0 ? "Empty file" : "No text to show"} />;
  return (
    <>
      {file.truncated && (
        <p className="sticky left-0 border-b border-status-warning/30 bg-status-warning/10 px-4 py-1.5 text-xs text-status-warning" role="note">
          Showing the first 1 MiB of {formatBytes(file.size)}.
        </p>
      )}
      <CodeView code={file.content} path={file.relative || path} line={line} />
    </>
  );
}

// A file with line numbers, highlighted for its type; line is scrolled to
// and marked. The numbers stay in view while long lines scroll sideways.
function CodeView({code, path, line}: {code: string; path: string; line?: number}) {
  const lines = useMemo(() => highlightCode(code, languageForPath(path)), [code, path]);
  const target = line !== undefined && line >= 1 && line <= lines.length ? line : undefined;
  const targetRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    targetRef.current?.scrollIntoView({block: "center"});
  }, [target, lines]);
  const gutterWidth = `calc(${String(lines.length).length}ch + 1.5rem)`;
  return (
    <div className="min-h-full min-w-max bg-term py-2 font-mono text-[12.5px] leading-5 text-term-foreground [tab-size:4]">
      {lines.map((tokens, index) => {
        const number = index + 1;
        const isTarget = number === target;
        return (
          <div key={number} ref={isTarget ? targetRef : undefined} className={cn("flex", isTarget && "bg-state-working/20")}>
            <span
              aria-hidden="true"
              style={{width: gutterWidth}}
              className={cn(
                "sticky left-0 shrink-0 select-none bg-term px-3 text-right tabular-nums text-term-dim",
                isTarget &&
                  "bg-[color-mix(in_oklab,var(--state-working)_20%,var(--term))] text-term-foreground shadow-[inset_3px_0_0_var(--state-working)]",
              )}
            >
              {number}
            </span>
            <span className="whitespace-pre pl-1 pr-6">{tokens.length > 0 ? tokens.map(renderToken) : " "}</span>
          </div>
        );
      })}
    </div>
  );
}

function renderToken(token: CodeToken, index: number): ReactNode {
  const content = token.classes.reduceRight<ReactNode>(
    (child, className) => <span className={className}>{child}</span>,
    token.text,
  );
  return <Fragment key={index}>{content}</Fragment>;
}

const STATUS_LABEL: Partial<Record<FileDiffStatus, {label: string; title: string}>> = {
  modified: {label: "Modified", title: "Changed since the last commit"},
  added: {label: "Added", title: "Added to git (staged) but not committed"},
  deleted: {label: "Deleted", title: "Deleted since the last commit"},
  untracked: {label: "Untracked", title: "Not in git yet; shown as added"},
};

function DiffPane({loaded, change, onRetry}: {loaded: Loaded<FileChanges> | null; change?: string; onRetry: () => void}) {
  const changes = loaded?.state === "ready" ? loaded.data : undefined;
  const status = changes ? STATUS_LABEL[changes.status] : undefined;
  let uncommitted: ReactNode;
  if (!loaded || loaded.state === "loading") {
    uncommitted = <Loading compact>Loading the changes…</Loading>;
  } else if (loaded.state === "error") {
    uncommitted = <LoadError error={loaded.error} subject="changes" onRetry={onRetry} compact />;
  } else if (loaded.data.status === "not_in_repo") {
    uncommitted = <Notice compact icon={GitCompare} title="Not in a git repository" text="There is no commit to compare the file with." />;
  } else if (loaded.data.status === "unchanged") {
    uncommitted = <Notice compact icon={GitCompare} title="No uncommitted changes" text="The file is as it was in the last commit." />;
  } else if (!loaded.data.diff) {
    uncommitted = <Notice compact icon={GitCompare} title="No changes to show" />;
  } else {
    uncommitted = (
      <>
        <DiffView diff={loaded.data.diff} label="Uncommitted changes" />
        {loaded.data.truncated && <p className="text-xs text-status-warning">The diff is cut at 1 MiB.</p>}
      </>
    );
  }
  return (
    <div className="space-y-6 px-3 py-3 sm:px-4">
      {change && (
        <section className="space-y-1.5" aria-label="This change">
          <SectionHeading title="This change" note="from the tool call" copy={change} />
          <DiffView diff={change} label="This change" />
        </section>
      )}
      <section className="space-y-1.5" aria-label="Uncommitted changes">
        <SectionHeading
          title="Uncommitted changes"
          badge={
            status && (
              <span className="rounded-[4px] border px-1.5 font-mono text-[11px] leading-[18px] text-muted-foreground" title={status.title}>
                {status.label}
              </span>
            )
          }
          copy={changes?.diff || undefined}
        />
        {uncommitted}
      </section>
    </div>
  );
}

function SectionHeading({title, note, badge, copy}: {title: string; note?: string; badge?: ReactNode; copy?: string}) {
  return (
    <div className="flex min-h-9 items-center gap-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</h3>
      {note && <span className="text-xs text-muted-foreground">{note}</span>}
      {badge}
      {copy && (
        <span className="ml-auto">
          <CopyButton content={copy} label="diff" />
        </span>
      )}
    </div>
  );
}

function Loading({children, compact = false}: {children: ReactNode; compact?: boolean}) {
  return (
    <p role="status" className={cn("flex items-center gap-2 text-sm text-muted-foreground", compact ? "py-2" : "justify-center px-6 py-16")}>
      <LoaderCircle className="size-4 shrink-0 motion-safe:animate-spin" />
      {children}
    </p>
  );
}

function Notice({
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

function LoadError({error, subject, onRetry, compact}: {error: unknown; subject: "file" | "changes"; onRetry: () => void; compact?: boolean}) {
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
  if (status === 404 && subject === "file") {
    return <Notice alert compact={compact} icon={FileQuestion} title="File not found" text="It may have been deleted, moved or renamed." detail={detail} onRetry={onRetry} />;
  }
  return (
    <Notice
      alert
      compact={compact}
      icon={TriangleAlert}
      title={subject === "file" ? "Couldn't show the file" : "Couldn't show the changes"}
      detail={detail}
      // A bad request (a directory, an empty path) stays bad.
      onRetry={status === 400 ? undefined : onRetry}
    />
  );
}

// "12,345 bytes (12.1 KB)".
function formatBytes(bytes: number): string {
  const exact = `${bytes.toLocaleString("en-US")} ${bytes === 1 ? "byte" : "bytes"}`;
  if (bytes < 1024) return exact;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${exact} (${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]})`;
}
