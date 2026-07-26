"use client";

import {useMemo, useState} from "react";
import {
  ExternalLink,
  FileText,
  FolderSearch,
  Link as LinkIcon,
  ListTree,
  LoaderCircle,
  RefreshCw,
  SquareTerminal,
} from "lucide-react";
import {toast} from "sonner";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./ui/dialog";
import {Tabs, TabsContent, TabsList, TabsTrigger} from "./ui/tabs";

interface ExplorerProps {
  onNavigateTask: (number: number) => void;
}

interface DiscoveredLink {
  url: string;
  task: number;
}

const pathPattern = /(?:^|[\s"'(])((?:\/[\w.@+-]+)+\.[a-zA-Z0-9]{1,10})(?=$|[\s"'),:;])/g;

export function Explorer({onNavigateTask}: ExplorerProps) {
  const {
    messages,
    backgroundTasks,
    refreshBackgroundTasks,
    getBackgroundTaskOutput,
  } = useChat();
  const [open, setOpen] = useState(false);
  const [loadingTask, setLoadingTask] = useState<string | null>(null);
  const [outputs, setOutputs] = useState<Record<string, string>>({});
  const tasks = useMemo(
    () => messages.filter((message) => message.role === "user"),
    [messages],
  );
  const links = useMemo(() => discoverLinks(messages), [messages]);
  const files = useMemo(() => {
    const found = new Map<string, number>();
    let task = 0;
    for (const message of messages) {
      if (message.role === "user") task += 1;
      for (const match of message.content.matchAll(pathPattern)) {
        if (!found.has(match[1])) found.set(match[1], task);
      }
    }
    for (const backgroundTask of backgroundTasks) {
      if (backgroundTask.output_path && !found.has(backgroundTask.output_path)) {
        found.set(backgroundTask.output_path, 0);
      }
    }
    return [...found].map(([path, sourceTask]) => ({path, sourceTask}));
  }, [backgroundTasks, messages]);

  const navigate = (number: number) => {
    setOpen(false);
    onNavigateTask(number);
  };
  const loadOutput = async (id: string) => {
    setLoadingTask(id);
    try {
      const output = await getBackgroundTaskOutput(id);
      setOutputs((current) => ({
        ...current,
        [id]: `${output.truncated ? "…showing tail…\n" : ""}${output.content}`,
      }));
    } catch {
      toast.error("Background task output is unavailable");
    } finally {
      setLoadingTask(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="outline" className="h-9">
          <FolderSearch />
          <span className="hidden sm:inline">Explorer</span>
          <span className="sr-only sm:hidden">Open explorer</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="left-auto right-0 top-0 h-dvh max-h-dvh w-full max-w-xl translate-x-0 translate-y-0 content-start overflow-hidden rounded-none p-0 sm:rounded-none">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle>Session Explorer</DialogTitle>
          <DialogDescription>
            Background tasks, reconstructed links, files, and task navigation.
          </DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="background" className="min-h-0 gap-0">
          <div className="overflow-x-auto border-b px-3 py-2">
            <TabsList className="grid w-full min-w-[430px] grid-cols-4">
              <TabsTrigger value="background"><SquareTerminal />Tasks</TabsTrigger>
              <TabsTrigger value="links"><LinkIcon />Links</TabsTrigger>
              <TabsTrigger value="files"><FileText />Files</TabsTrigger>
              <TabsTrigger value="index"><ListTree />Index</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="background" className="min-h-0 overflow-y-auto p-4">
            <div className="mb-3 flex items-center justify-between">
              <p className="text-xs text-muted-foreground">
                {backgroundTasks.length} discovered background tasks
              </p>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => void refreshBackgroundTasks()}
              >
                <RefreshCw />Refresh
              </Button>
            </div>
            <div className="space-y-3">
              {backgroundTasks.map((task) => (
                <article key={task.id} className="rounded-xl border bg-card p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="break-words text-sm font-medium">{task.name}</p>
                      <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                        {task.agent_type} · {task.id}
                      </p>
                    </div>
                    <TaskStatus status={task.status} />
                  </div>
                  {task.output_path && (
                    <>
                      <p className="mt-3 break-all font-mono text-[11px] text-muted-foreground">
                        {task.output_path}
                      </p>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        className="mt-2"
                        onClick={() => void loadOutput(task.id)}
                        disabled={loadingTask === task.id}
                      >
                        {loadingTask === task.id
                          ? <LoaderCircle className="animate-spin" />
                          : <FileText />}
                        Read output
                      </Button>
                    </>
                  )}
                  {outputs[task.id] !== undefined && (
                    <pre className="mt-3 max-h-72 overflow-auto whitespace-pre rounded-lg bg-zinc-950 p-3 font-mono text-xs text-zinc-100">
                      {outputs[task.id] || "(empty output)"}
                    </pre>
                  )}
                </article>
              ))}
              {backgroundTasks.length === 0 && <Empty text="No background tasks discovered yet." />}
            </div>
          </TabsContent>
          <TabsContent value="links" className="min-h-0 overflow-y-auto p-4">
            <div className="space-y-2">
              {links.map((link) => (
                <div key={`${link.task}-${link.url}`} className="rounded-xl border p-3">
                  <a
                    href={link.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-start gap-2 break-all text-sm underline underline-offset-4"
                  >
                    <ExternalLink className="mt-0.5 size-4 shrink-0" />
                    {link.url}
                  </a>
                  {link.task > 0 && (
                    <button
                      type="button"
                      className="mt-2 text-xs text-muted-foreground hover:text-foreground"
                      onClick={() => navigate(link.task)}
                    >
                      Go to Task {link.task}
                    </button>
                  )}
                </div>
              ))}
              {links.length === 0 && <Empty text="No links found in this session." />}
            </div>
          </TabsContent>
          <TabsContent value="files" className="min-h-0 overflow-y-auto p-4">
            <div className="space-y-2">
              {files.map((file) => (
                <button
                  key={file.path}
                  type="button"
                  className="block w-full rounded-xl border p-3 text-left hover:bg-muted/40"
                  onClick={() => file.sourceTask > 0 && navigate(file.sourceTask)}
                >
                  <span className="block break-all font-mono text-xs">{file.path}</span>
                  <span className="mt-1 block text-[11px] text-muted-foreground">
                    {file.sourceTask > 0 ? `Task ${file.sourceTask}` : "Background task output"}
                  </span>
                </button>
              ))}
              {files.length === 0 && <Empty text="No file paths found in this session." />}
            </div>
          </TabsContent>
          <TabsContent value="index" className="min-h-0 overflow-y-auto p-4">
            <div className="space-y-2">
              {tasks.map((task, index) => (
                <button
                  key={task.id ?? index}
                  type="button"
                  className="block w-full rounded-xl border p-3 text-left hover:bg-muted/40"
                  onClick={() => navigate(index + 1)}
                >
                  <span className="text-[11px] font-semibold uppercase text-muted-foreground">
                    Task {index + 1}
                  </span>
                  <span className="mt-1 block line-clamp-2 text-sm">{task.content}</span>
                </button>
              ))}
            </div>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

function TaskStatus({status}: {status: string}) {
  const color =
    status === "running"
      ? "bg-amber-500/15 text-amber-700 dark:text-amber-300"
      : status === "completed"
        ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
        : status === "failed"
          ? "bg-destructive/15 text-destructive"
          : "bg-muted text-muted-foreground";
  return <span className={`rounded-full px-2 py-1 text-[11px] font-medium ${color}`}>{status}</span>;
}

function Empty({text}: {text: string}) {
  return <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">{text}</p>;
}

function discoverLinks(messages: Array<{role: string; content: string}>) {
  const found = new Map<string, DiscoveredLink>();
  let task = 0;
  for (const message of messages) {
    if (message.role === "user") task += 1;
    for (const url of reconstructWrappedURLs(message.content)) {
      if (!found.has(url)) found.set(url, {url, task});
    }
  }
  return [...found.values()];
}

export function reconstructWrappedURLs(content: string) {
  const lines = content.split("\n");
  const urls: string[] = [];
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const line = lines[lineIndex];
    const starts = [...line.matchAll(/https?:\/\//g)].map((match) => match.index ?? 0);
    for (const start of starts) {
      let candidate = line.slice(start).match(/^\S+/)?.[0] ?? "";
      const reachesLineEnd = start + candidate.length === line.trimEnd().length;
      let next = lineIndex + 1;
      while (
        reachesLineEnd &&
        next < lines.length &&
        (lines[next - 1].trimEnd().length >= 72 ||
          /[/?#&=._~%+-]$/.test(candidate)) &&
        candidate.length > 0 &&
        /^[A-Za-z0-9/?#&=._~%+:@!,;()[\]-]+$/.test(lines[next].trim()) &&
        !/\s/.test(lines[next].trim())
      ) {
        candidate += lines[next].trim();
        next += 1;
      }
      candidate = candidate.replace(/[),.;:!?]+$/u, "");
      try {
        const parsed = new URL(candidate);
        if (parsed.protocol === "http:" || parsed.protocol === "https:") {
          urls.push(parsed.toString());
        }
      } catch {
        // Ignore text that only looked like a URL.
      }
    }
  }
  return [...new Set(urls)];
}
