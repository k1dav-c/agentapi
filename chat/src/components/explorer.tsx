"use client";

import {useEffect, useMemo, useRef, useState} from "react";
import {
  Activity,
  BellRing,
  Download,
  ExternalLink,
  FileText,
  FolderSearch,
  Link as LinkIcon,
  ListTree,
  LoaderCircle,
  Play,
  RefreshCw,
  Save,
  Server,
  Trash2,
  Upload,
} from "lucide-react";
import {toast} from "sonner";
import {editableMCPServers} from "@/lib/mcp-sample";
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
    getWebhook,
    updateWebhook,
    getMCP,
    updateMCP,
    checkMCP,
    getMCPProfiles,
    saveMCPProfile,
    deleteMCPProfile,
    applyMCPProfile,
  } = useChat();
  const [open, setOpen] = useState(false);
  const [activeTab, setActiveTab] = useState("links");
  const [mcpJSON, setMCPJSON] = useState("{}");
  const [mcpIsSample, setMCPIsSample] = useState(false);
  const [mcpPath, setMCPPath] = useState("");
  const [mcpSupported, setMCPSupported] = useState(true);
  const [mcpLoading, setMCPLoading] = useState(false);
  const [mcpSaving, setMCPSaving] = useState(false);
  const [mcpChecking, setMCPChecking] = useState(false);
  const [mcpChecks, setMCPChecks] = useState<Array<{name: string; status: string; detail: string; kind: string; latency_ms?: number}>>([]);
  const [profiles, setProfiles] = useState<Record<string, Record<string, unknown>>>({});
  const [profileName, setProfileName] = useState("");
  const [restartAfterSave, setRestartAfterSave] = useState(true);
  const [webhookURL, setWebhookURL] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [webhookSecretConfigured, setWebhookSecretConfigured] = useState(false);
  const [webhookClearSecret, setWebhookClearSecret] = useState(false);
  const [webhookTimeout, setWebhookTimeout] = useState(10);
  const [webhookMaxAttempts, setWebhookMaxAttempts] = useState(3);
  const [webhookLoading, setWebhookLoading] = useState(false);
  const [webhookSaving, setWebhookSaving] = useState(false);
  const mcpImportRef = useRef<HTMLInputElement>(null);
  const profileImportRef = useRef<HTMLInputElement>(null);
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
    return [...found].map(([path, sourceTask]) => ({path, sourceTask}));
  }, [messages]);

  const navigate = (number: number) => {
    setOpen(false);
    onNavigateTask(number);
  };
  const loadMCP = async () => {
    setMCPLoading(true);
    try {
      const config = await getMCP();
      const editable = editableMCPServers(config.servers);
      setMCPJSON(JSON.stringify(editable.servers, null, 2));
      setMCPIsSample(editable.isSample);
      setMCPPath(config.path);
      setMCPSupported(true);
      const profileData = await getMCPProfiles();
      setProfiles(profileData.profiles);
    } catch {
      setMCPSupported(false);
    } finally {
      setMCPLoading(false);
    }
  };
  const loadWebhook = async () => {
    setWebhookLoading(true);
    try {
      const config = await getWebhook();
      setWebhookURL(config.url);
      setWebhookSecret("");
      setWebhookSecretConfigured(config.secret_configured);
      setWebhookClearSecret(false);
      setWebhookTimeout(config.timeout_seconds);
      setWebhookMaxAttempts(config.max_attempts);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not load webhook configuration");
    } finally {
      setWebhookLoading(false);
    }
  };
  const saveWebhook = async () => {
    setWebhookSaving(true);
    try {
      const config = await updateWebhook({
        url: webhookURL.trim(),
        ...(webhookClearSecret
          ? {secret: ""}
          : webhookSecret !== ""
            ? {secret: webhookSecret}
            : {}),
        timeout_seconds: webhookTimeout,
        max_attempts: webhookMaxAttempts,
      });
      setWebhookURL(config.url);
      setWebhookSecret("");
      setWebhookSecretConfigured(config.secret_configured);
      setWebhookClearSecret(false);
      toast.success(config.url ? "Webhook configuration updated" : "Webhook disabled");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not update webhook configuration");
    } finally {
      setWebhookSaving(false);
    }
  };
  const parsedMCP = () => {
    const servers = JSON.parse(mcpJSON) as unknown;
    if (!servers || Array.isArray(servers) || typeof servers !== "object") {
      throw new Error("MCP configuration must be an object");
    }
    return servers as Record<string, unknown>;
  };
  const runMCPChecks = async () => {
    setMCPChecking(true);
    try {
      setMCPChecks(await checkMCP(parsedMCP()));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not check MCP servers");
    } finally {
      setMCPChecking(false);
    }
  };
  const saveProfile = async () => {
    const name = profileName.trim();
    if (!name) return toast.error("Enter a profile name");
    try {
      const result = await saveMCPProfile(name, parsedMCP());
      setProfiles(result.profiles);
      setProfileName("");
      toast.success(`Profile “${name}” saved`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not save profile");
    }
  };
  const applyProfile = async (name: string) => {
    try {
      await applyMCPProfile(name, restartAfterSave);
      await loadMCP();
      toast.success(`Profile “${name}” applied`);
    } catch {
      toast.error("Could not apply profile");
    }
  };
  const removeProfile = async (name: string) => {
    try {
      const result = await deleteMCPProfile(name);
      setProfiles(result.profiles);
    } catch {
      toast.error("Could not delete profile");
    }
  };
  const exportProfiles = () => {
    downloadJSON("agentapi-mcp-profiles.json", {profiles});
  };
  const exportMCP = () => {
    try {
      downloadJSON("agentapi-mcp-servers.json", {servers: parsedMCP()});
    } catch {
      toast.error("MCP configuration must be valid JSON");
    }
  };
  const importMCP = async (file: File) => {
    try {
      const value = JSON.parse(await file.text()) as {
        servers?: Record<string, unknown>;
      };
      const servers = value.servers ?? value;
      if (!servers || Array.isArray(servers) || typeof servers !== "object") {
        throw new Error();
      }
      setMCPJSON(JSON.stringify(servers, null, 2));
      setMCPIsSample(false);
      setMCPChecks([]);
      toast.success("MCP servers loaded for review", {
        description: "Save servers to write the imported configuration.",
      });
    } catch {
      toast.error("Invalid MCP server export");
    }
  };
  const importProfiles = async (file: File) => {
    try {
      const value = JSON.parse(await file.text()) as {profiles?: Record<string, Record<string, unknown>>};
      if (!value.profiles || typeof value.profiles !== "object") throw new Error();
      let latest = profiles;
      for (const [name, servers] of Object.entries(value.profiles)) {
        latest = (await saveMCPProfile(name, servers)).profiles;
      }
      setProfiles(latest);
      toast.success("MCP profiles imported");
    } catch {
      toast.error("Invalid MCP profile export");
    }
  };
  const saveMCP = async () => {
    let servers: unknown;
    try {
      servers = JSON.parse(mcpJSON);
    } catch {
      toast.error("MCP configuration must be valid JSON");
      return;
    }
    if (!servers || Array.isArray(servers) || typeof servers !== "object") {
      toast.error("MCP configuration must be a JSON object keyed by server name");
      return;
    }
    setMCPSaving(true);
    try {
      const config = await updateMCP(
        servers as Record<string, unknown>,
        restartAfterSave,
      );
      setMCPPath(config.path);
      setMCPJSON(JSON.stringify(config.servers, null, 2));
      setMCPIsSample(false);
      toast.success("MCP servers updated", {
        description: config.restarted
          ? "The agent restarted and is loading the new configuration."
          : "Changes apply when the agent starts its next session.",
      });
    } catch {
      toast.error("Could not update MCP servers");
    } finally {
      setMCPSaving(false);
    }
  };

  useEffect(() => {
    if (open && activeTab === "mcp") void loadMCP();
    if (open && activeTab === "webhook") void loadWebhook();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, open]);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="outline" className="h-9">
          <FolderSearch />
          <span className="hidden sm:inline">Explorer</span>
          <span className="sr-only sm:hidden">Open explorer</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="left-auto right-0 top-0 flex h-dvh max-h-dvh w-full max-w-xl translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none p-0 sm:rounded-none">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle>Session Explorer</DialogTitle>
          <DialogDescription>
            Links, files, task navigation, MCP, and webhook configuration.
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={activeTab}
          onValueChange={setActiveTab}
          className="min-h-0 flex-1 gap-0"
        >
          <div className="border-b px-3 py-2">
            <TabsList className="grid w-full grid-cols-5">
              <TabsTrigger value="links"><LinkIcon /><span className="max-sm:sr-only">Links</span></TabsTrigger>
              <TabsTrigger value="files"><FileText /><span className="max-sm:sr-only">Files</span></TabsTrigger>
              <TabsTrigger value="index"><ListTree /><span className="max-sm:sr-only">Index</span></TabsTrigger>
              <TabsTrigger value="mcp"><Server /><span className="max-sm:sr-only">MCP</span></TabsTrigger>
              <TabsTrigger value="webhook"><BellRing /><span className="max-sm:sr-only">Webhook</span></TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="links" className="min-h-0 overflow-y-auto overscroll-contain p-4">
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
          <TabsContent value="files" className="min-h-0 overflow-y-auto overscroll-contain p-4">
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
                    {`Task ${file.sourceTask}`}
                  </span>
                </button>
              ))}
              {files.length === 0 && <Empty text="No file paths found in this session." />}
            </div>
          </TabsContent>
          <TabsContent value="index" className="min-h-0 overflow-y-auto overscroll-contain p-4">
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
          <TabsContent value="mcp" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            {mcpLoading ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <LoaderCircle className="size-4 animate-spin" />
                Loading MCP servers…
              </div>
            ) : !mcpSupported ? (
              <Empty text="MCP config management is available only for Claude and Codex." />
            ) : (
              <div className="space-y-4">
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <h3 className="text-sm font-medium">MCP servers</h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      Enter the complete server map as JSON. Saving replaces the
                      existing MCP server set.
                    </p>
                  </div>
                  <div className="flex shrink-0">
                    <input
                      ref={mcpImportRef}
                      type="file"
                      accept="application/json,.json"
                      className="hidden"
                      onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) void importMCP(file);
                        event.target.value = "";
                      }}
                    />
                    <Button type="button" size="icon" variant="ghost" title="Import MCP servers" onClick={() => mcpImportRef.current?.click()}>
                      <Upload />
                    </Button>
                    <Button type="button" size="icon" variant="ghost" title="Export MCP servers" onClick={exportMCP}>
                      <Download />
                    </Button>
                  </div>
                </div>
                <textarea
                  value={mcpJSON}
                  onChange={(event) => {
                    setMCPJSON(event.target.value);
                    setMCPIsSample(false);
                  }}
                  spellCheck={false}
                  aria-label="MCP server configuration"
                  className="min-h-80 w-full resize-y rounded-xl border bg-zinc-950 p-3 font-mono text-xs leading-5 text-zinc-100 outline-none focus:ring-2 focus:ring-ring"
                />
                {mcpIsSample && (
                  <p className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-800 dark:text-amber-200">
                    Samples only — replace the filesystem path and remote
                    Streamable HTTP URL with real values, then save to add these
                    MCP servers.
                  </p>
                )}
                <div className="space-y-2 rounded-xl border p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div>
                      <p className="text-sm font-medium">Connectivity</p>
                      <p className="text-xs text-muted-foreground">HTTP servers are probed; stdio executables are resolved in PATH.</p>
                    </div>
                    <Button type="button" size="sm" variant="outline" onClick={() => void runMCPChecks()} disabled={mcpChecking}>
                      {mcpChecking ? <LoaderCircle className="animate-spin" /> : <Activity />}
                      Check
                    </Button>
                  </div>
                  {mcpChecks.map((check) => (
                    <div key={check.name} className="flex items-start justify-between gap-3 rounded-lg bg-muted/40 p-2 text-xs">
                      <div className="min-w-0"><p className="font-medium">{check.name} · {check.kind}</p><p className="break-all text-muted-foreground">{check.detail}</p></div>
                      <TaskStatus status={check.status} />
                    </div>
                  ))}
                </div>
                <div className="space-y-3 rounded-xl border p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div><p className="text-sm font-medium">Profiles</p><p className="text-xs text-muted-foreground">Save, apply, import, or export complete server sets.</p></div>
                    <div className="flex">
                      <input ref={profileImportRef} type="file" accept="application/json,.json" className="hidden" onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) void importProfiles(file);
                        event.target.value = "";
                      }} />
                      <Button type="button" size="icon" variant="ghost" title="Import profiles" onClick={() => profileImportRef.current?.click()}><Upload /></Button>
                      <Button type="button" size="icon" variant="ghost" title="Export profiles" onClick={exportProfiles}><Download /></Button>
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <input value={profileName} onChange={(event) => setProfileName(event.target.value)} placeholder="Profile name" className="min-w-0 flex-1 rounded-md border bg-background px-3 text-sm" />
                    <Button type="button" size="sm" variant="outline" onClick={() => void saveProfile()}><Save />Save</Button>
                  </div>
                  {Object.keys(profiles).sort().map((name) => (
                    <div key={name} className="flex items-center justify-between rounded-lg bg-muted/40 px-3 py-2">
                      <span className="truncate text-sm">{name} <span className="text-xs text-muted-foreground">({Object.keys(profiles[name]).length})</span></span>
                      <div className="flex">
                        <Button type="button" size="icon" variant="ghost" title={`Apply ${name}`} onClick={() => void applyProfile(name)}><Play /></Button>
                        <Button type="button" size="icon" variant="ghost" title={`Delete ${name}`} onClick={() => void removeProfile(name)}><Trash2 /></Button>
                      </div>
                    </div>
                  ))}
                </div>
                {mcpPath && (
                  <p className="break-all font-mono text-[11px] text-muted-foreground">
                    {mcpPath}
                  </p>
                )}
                <label className="flex items-start gap-3 rounded-xl border bg-muted/20 p-3">
                  <input
                    type="checkbox"
                    checked={restartAfterSave}
                    onChange={(event) =>
                      setRestartAfterSave(event.target.checked)
                    }
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span>
                    <span className="block text-sm font-medium">
                      Restart agent after saving
                    </span>
                    <span className="mt-1 block text-xs leading-5 text-muted-foreground">
                      Applies the MCP configuration immediately, but resets the
                      agent&apos;s current conversation context.
                    </span>
                  </span>
                </label>
                <div className="flex items-center justify-between gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => void loadMCP()}
                    disabled={mcpLoading || mcpSaving}
                  >
                    <RefreshCw />
                    Reload
                  </Button>
                  <Button
                    type="button"
                    onClick={() => void saveMCP()}
                    disabled={mcpLoading || mcpSaving}
                  >
                    {mcpSaving ? (
                      <LoaderCircle className="animate-spin" />
                    ) : (
                      <Save />
                    )}
                    Save servers
                  </Button>
                </div>
                <p className="text-[11px] leading-5 text-muted-foreground">
                  Without restart, Claude and Codex load the configuration in
                  their next session.
                </p>
              </div>
            )}
          </TabsContent>
          <TabsContent value="webhook" className="min-h-0 overflow-y-auto overscroll-contain p-4">
            {webhookLoading ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <LoaderCircle className="size-4 animate-spin" />
                Loading webhook configuration…
              </div>
            ) : (
              <div className="space-y-4">
                <div>
                  <h3 className="text-sm font-medium">Run status webhook</h3>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    Send a signed HTTP POST when the run changes between running and stable.
                    Startup flags provide the initial values; changes here apply immediately.
                  </p>
                </div>
                <label className="block space-y-1.5">
                  <span className="text-xs font-medium">Webhook URL</span>
                  <input
                    type="url"
                    value={webhookURL}
                    onChange={(event) => setWebhookURL(event.target.value)}
                    placeholder="https://example.com/agentapi/events"
                    className="h-10 w-full rounded-md border bg-background px-3 text-sm"
                  />
                  <span className="block text-[11px] text-muted-foreground">
                    Leave empty and save to disable delivery.
                  </span>
                </label>
                <label className="block space-y-1.5">
                  <span className="text-xs font-medium">Signing secret</span>
                  <input
                    type="password"
                    value={webhookSecret}
                    onChange={(event) => {
                      setWebhookSecret(event.target.value);
                      setWebhookClearSecret(false);
                    }}
                    disabled={webhookClearSecret}
                    placeholder={webhookSecretConfigured ? "Configured — leave blank to keep" : "Optional HMAC secret"}
                    autoComplete="new-password"
                    className="h-10 w-full rounded-md border bg-background px-3 text-sm"
                  />
                  <span className="block text-[11px] text-muted-foreground">
                    Secrets are write-only and are never returned by the API.
                  </span>
                </label>
                {webhookSecretConfigured && (
                  <label className="flex items-center gap-2 text-xs text-muted-foreground">
                    <input
                      type="checkbox"
                      checked={webhookClearSecret}
                      onChange={(event) => {
                        setWebhookClearSecret(event.target.checked);
                        if (event.target.checked) setWebhookSecret("");
                      }}
                      className="size-4 accent-primary"
                    />
                    Clear the configured signing secret
                  </label>
                )}
                <div className="grid grid-cols-2 gap-3">
                  <label className="block space-y-1.5">
                    <span className="text-xs font-medium">Timeout (seconds)</span>
                    <input
                      type="number"
                      min={1}
                      max={3600}
                      value={webhookTimeout}
                      onChange={(event) => setWebhookTimeout(Number(event.target.value))}
                      className="h-10 w-full rounded-md border bg-background px-3 text-sm"
                    />
                  </label>
                  <label className="block space-y-1.5">
                    <span className="text-xs font-medium">Max attempts</span>
                    <input
                      type="number"
                      min={1}
                      max={10}
                      value={webhookMaxAttempts}
                      onChange={(event) => setWebhookMaxAttempts(Number(event.target.value))}
                      className="h-10 w-full rounded-md border bg-background px-3 text-sm"
                    />
                  </label>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <Button type="button" variant="ghost" onClick={() => void loadWebhook()} disabled={webhookLoading || webhookSaving}>
                    <RefreshCw />
                    Reload
                  </Button>
                  <Button type="button" onClick={() => void saveWebhook()} disabled={webhookLoading || webhookSaving}>
                    {webhookSaving ? <LoaderCircle className="animate-spin" /> : <Save />}
                    Save webhook
                  </Button>
                </div>
              </div>
            )}
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

function downloadJSON(filename: string, value: unknown) {
  const link = document.createElement("a");
  link.href = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], {type: "application/json"}),
  );
  link.download = filename;
  link.click();
  URL.revokeObjectURL(link.href);
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
