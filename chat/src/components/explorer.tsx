"use client";

import {useEffect, useMemo, useState} from "react";
import {discoverFiles, discoverLinks} from "@/lib/explorer-index";
import {Download, ExternalLink, FileText, FolderSearch, Link as LinkIcon, ListTree, LoaderCircle, RotateCw, Server, Webhook as WebhookIcon} from "lucide-react";
import {toast} from "sonner";
import {agentDisplayName, updatesOnRestart} from "@/lib/agents";
import {useChatActions, useChatSelector} from "./chat-provider";
import type {DraftMessage, Message} from "@/lib/conversation";
import type {ServerTask} from "@/lib/server-tasks";
import {filterTasks} from "@/lib/task-filter";
import {TASK_STATUS_META, type Timeline} from "@/lib/task-timeline";
import {MCPSettings, useMCPSettings} from "./explorer-mcp";
import {useFileViewer} from "./file-viewer-context";
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
import {WebhookSettings} from "./webhook-settings";
import {SessionHistory} from "./session-history";
import {Empty} from "./explorer-empty";

interface ExplorerProps {
  onNavigateTask: (number: number) => void;
}

// What the Explorer lists while it lists no messages: selecting the
// messages only then, it doesn't render again as they stream.
const NO_MESSAGES: (Message | DraftMessage)[] = [];
const NO_TIMELINE: Timeline = {prelude: [], tasks: []};
const NO_TASKS: ReadonlyMap<number, ServerTask> = new Map();

export function Explorer({onNavigateTask}: ExplorerProps) {
  const features = useChatSelector((chat) => chat.features);
  const agentType = useChatSelector((chat) => chat.agentType);
  const {deleteMessages, downloadSession} = useChatActions();
  const [downloading, setDownloading] = useState(false);
  const {restartKeepsConversation} = features;
  const fileViewer = useFileViewer();
  const [open, setOpen] = useState(false);
  const [activeTab, setActiveTab] = useState("index");
  const [filter, setFilter] = useState("");
  const [confirmRestart, setConfirmRestart] = useState(false);
  const matches = (text: string) => text.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase());

  // ⌘K / Ctrl+K opens the Explorer on task navigation.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setActiveTab("index");
        setOpen((value) => !value);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // Restart asks for a second click instead of a browser confirm dialog.
  useEffect(() => {
    if (!confirmRestart) return;
    const timer = window.setTimeout(() => setConfirmRestart(false), 4000);
    return () => window.clearTimeout(timer);
  }, [confirmRestart]);
  const [restartingAgent, setRestartingAgent] = useState(false);
  // The MCP tab keeps what it shows while the Explorer is closed or on
  // another tab.
  const mcp = useMCPSettings(open && activeTab === "mcp");
  // Every list is newest first; items keep their task number. Only the
  // tab that shows is listed, while the Explorer is open: the messages
  // change many times a second while the agent works.
  const shown = open ? activeTab : null;
  const listed = shown === "links" || shown === "files";
  const indexed = shown === "index";
  const messages = useChatSelector((chat) => (listed ? chat.messages : NO_MESSAGES));
  // The index lists the tasks with their status, as the transcript has it.
  const timeline = useChatSelector((chat) => (indexed ? chat.timeline : NO_TIMELINE));
  const serverStatus = useChatSelector((chat) => (indexed ? chat.serverStatus : "unknown"));
  const taskByMessage = useChatSelector((chat) => (indexed ? chat.taskByMessage : NO_TASKS));
  const tasks = useMemo(
    () =>
      filterTasks(timeline.tasks, serverStatus, "all", "", taskByMessage)
        .map(({task, index, status}) => ({message: task.prompt, number: index + 1, status}))
        .reverse(),
    [serverStatus, taskByMessage, timeline],
  );
  const links = useMemo(() => (shown === "links" ? discoverLinks(messages).reverse() : []), [messages, shown]);
  const files = useMemo(() => (shown === "files" ? discoverFiles(messages).reverse() : []), [messages, shown]);

  const navigate = (number: number) => {
    setOpen(false);
    onNavigateTask(number);
  };
  const handleRestart = async () => {
    if (!confirmRestart) {
      setConfirmRestart(true);
      return;
    }
    setConfirmRestart(false);
    setRestartingAgent(true);
    try {
      await deleteMessages();
      toast.success("Messages cleared and agent restarted");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not restart the agent");
    } finally {
      setRestartingAgent(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="size-8 rounded-full text-muted-foreground"
          title="Explorer (⌘K)"
        >
          <FolderSearch className="size-4" />
          <span className="sr-only">Open explorer</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="left-auto right-0 top-0 flex h-dvh max-h-dvh w-full max-w-xl translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none p-0 sm:rounded-none">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle>Explorer</DialogTitle>
          <DialogDescription>
            Jump to a task or link, or open a file. Agent settings are under MCP, Session and Webhook.
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={activeTab}
          onValueChange={setActiveTab}
          className="min-h-0 flex-1 gap-0"
        >
          <div className="border-b px-3 py-2">
            <TabsList className="grid w-full grid-cols-6">
              <TabsTrigger value="index"><ListTree /><span className="max-sm:sr-only">Index</span></TabsTrigger>
              <TabsTrigger value="files"><FileText /><span className="max-sm:sr-only">Files</span></TabsTrigger>
              <TabsTrigger value="links"><LinkIcon /><span className="max-sm:sr-only">Links</span></TabsTrigger>
              <TabsTrigger value="mcp"><Server /><span className="max-sm:sr-only">MCP</span></TabsTrigger>
              <TabsTrigger value="session"><RotateCw /><span className="max-sm:sr-only">Session</span></TabsTrigger>
              <TabsTrigger value="webhook"><WebhookIcon /><span className="max-sm:sr-only">Webhook</span></TabsTrigger>
            </TabsList>
            {["index", "files", "links"].includes(activeTab) && (
              <input
                autoFocus
                type="search"
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
                placeholder={`Filter ${activeTab === "index" ? "tasks" : activeTab}…`}
                aria-label={`Filter ${activeTab}`}
                className="mt-2 h-8 w-full rounded-md border bg-card px-2.5 text-sm outline-none focus:ring-2 focus:ring-ring"
              />
            )}
          </div>
          <TabsContent value="links" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            <div className="divide-y">
              {links.filter((link) => matches(link.url)).map((link) => (
                <div key={`${link.task}-${link.url}`} className="px-1 py-2.5">
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
                      className="mt-2 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
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
          <TabsContent value="files" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            <div className="divide-y">
              {files.filter((file) => matches(file.path)).map((file) => (
                // The path opens the file (over the Explorer, which is
                // still there after it closes); the task is a second button.
                <div key={file.path} className="px-1 py-2.5">
                  <button
                    type="button"
                    className="flex w-full items-start gap-2 rounded-sm text-left outline-none hover:underline hover:underline-offset-4 focus-visible:ring-2 focus-visible:ring-ring"
                    aria-haspopup={fileViewer ? "dialog" : undefined}
                    onClick={() =>
                      fileViewer ? fileViewer.open({path: file.path}) : file.sourceTask > 0 && navigate(file.sourceTask)
                    }
                  >
                    <FileText className="mt-px size-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 break-all font-mono text-xs leading-5">{file.path}</span>
                  </button>
                  {file.sourceTask > 0 && (
                    <button
                      type="button"
                      className="mt-1.5 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() => navigate(file.sourceTask)}
                    >
                      Go to Task {file.sourceTask}
                    </button>
                  )}
                </div>
              ))}
              {files.length === 0 && <Empty text="No file paths found in this session." />}
            </div>
          </TabsContent>
          <TabsContent value="index" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            <div className="divide-y">
              {tasks.filter(({message, number}) => matches(`${number} ${message.content}`)).map(({message, number, status}) => (
                <button
                  key={message.id ?? `task-${number}`}
                  type="button"
                  className="block w-full rounded-sm px-1 py-2.5 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={() => navigate(number)}
                >
                  <span className="flex items-center gap-2.5 text-[11px] font-semibold uppercase text-muted-foreground">
                    Task {number}
                    <span className={`flex items-center gap-1.5 font-medium normal-case ${TASK_STATUS_META[status].className}`} data-task-status={status}>
                      <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
                      {TASK_STATUS_META[status].label}
                    </span>
                  </span>
                  <span className="mt-1 block line-clamp-2 text-sm">{message.content}</span>
                </button>
              ))}
            </div>
          </TabsContent>
          <TabsContent value="mcp" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            <MCPSettings mcp={mcp} restartKeepsConversation={restartKeepsConversation} />
          </TabsContent>
          <TabsContent value="session" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            {features.sessionHistory && (
              <div className="mb-6">
                <SessionHistory active={shown === "session"} onOpened={() => setOpen(false)} />
              </div>
            )}
            <div className="space-y-3">
              <h3 className="text-sm font-semibold">Restart the agent</h3>
              <p className="text-sm text-muted-foreground">
                Stops the agent process, clears this conversation and starts the agent again.
                {updatesOnRestart(agentType) && ` ${agentDisplayName(agentType, "The agent")} is updated to its latest release first.`}
              </p>
              <Button
                type="button"
                variant={confirmRestart ? "destructive" : "outline"}
                disabled={restartingAgent}
                onClick={() => void handleRestart()}
              >
                {restartingAgent ? <LoaderCircle className="animate-spin" /> : <RotateCw />}
                {restartingAgent ? "Restarting…" : confirmRestart ? "Click again to restart" : "Restart agent"}
              </Button>
              <h3 className="pt-3 text-sm font-semibold">Download the conversation</h3>
              <p className="text-sm text-muted-foreground">
                Every message, tool call and result of this session as JSONL, one event per line, followed by those
                of the subagents the agent ran (each event says its subagent).
              </p>
              <Button
                type="button"
                variant="outline"
                disabled={downloading}
                onClick={() => {
                  setDownloading(true);
                  // The provider reports a failure with a toast.
                  void downloadSession().catch(() => undefined).finally(() => setDownloading(false));
                }}
              >
                {downloading ? <LoaderCircle className="animate-spin" /> : <Download />}
                Download conversation JSONL
              </Button>
            </div>
          </TabsContent>
          <TabsContent value="webhook" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            <WebhookSettings active={shown === "webhook"} />
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
