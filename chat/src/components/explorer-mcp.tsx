"use client";

import {useEffect, useRef, useState} from "react";
import {Activity, Download, LoaderCircle, Play, RefreshCw, Save, Trash2, Upload} from "lucide-react";
import {toast} from "sonner";
import {downloadFile} from "@/lib/download";
import {editableMCPServers} from "@/lib/mcp-sample";
import {useChatActions} from "./chat-provider";
import {Empty} from "./explorer";
import {Button} from "./ui/button";

// useMCPSettings is the state of the Explorer's MCP tab: the server map
// being edited, its checks and the profiles. The Explorer holds it, so it
// stays while the tab doesn't show; it loads the servers again each time
// the tab shows (active).
export function useMCPSettings(active: boolean) {
  const {getMCP, updateMCP, checkMCP, getMCPProfiles, saveMCPProfile, deleteMCPProfile, applyMCPProfile} = useChatActions();
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
  const mcpImportRef = useRef<HTMLInputElement>(null);
  const profileImportRef = useRef<HTMLInputElement>(null);
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
    } catch (error) {
      toast.error("Could not update MCP servers", {description: error instanceof Error ? error.message : undefined});
    } finally {
      setMCPSaving(false);
    }
  };

  useEffect(() => {
    if (active) void loadMCP();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  return {
    mcpJSON,
    setMCPJSON,
    mcpIsSample,
    setMCPIsSample,
    mcpPath,
    mcpSupported,
    mcpLoading,
    mcpSaving,
    mcpChecking,
    mcpChecks,
    profiles,
    profileName,
    setProfileName,
    restartAfterSave,
    setRestartAfterSave,
    mcpImportRef,
    profileImportRef,
    loadMCP,
    runMCPChecks,
    saveProfile,
    applyProfile,
    removeProfile,
    exportProfiles,
    exportMCP,
    importMCP,
    importProfiles,
    saveMCP,
  };
}

// MCPSettings is the Explorer's MCP tab: edit, check, import and export
// the agent's MCP servers, and keep sets of them as profiles.
export function MCPSettings({mcp, restartKeepsConversation}: {mcp: ReturnType<typeof useMCPSettings>; restartKeepsConversation: boolean}) {
  const {
    mcpJSON,
    setMCPJSON,
    mcpIsSample,
    setMCPIsSample,
    mcpPath,
    mcpSupported,
    mcpLoading,
    mcpSaving,
    mcpChecking,
    mcpChecks,
    profiles,
    profileName,
    setProfileName,
    restartAfterSave,
    setRestartAfterSave,
    mcpImportRef,
    profileImportRef,
    loadMCP,
    runMCPChecks,
    saveProfile,
    applyProfile,
    removeProfile,
    exportProfiles,
    exportMCP,
    importMCP,
    importProfiles,
    saveMCP,
  } = mcp;
  return mcpLoading ? (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      <LoaderCircle className="size-4 animate-spin" />
      Loading MCP servers…
    </div>
  ) : !mcpSupported ? (
    <Empty text="MCP config management is available only for Claude Code, Codex and Pi." />
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
        className="min-h-80 w-full resize-y rounded-xl border border-code-block-border bg-code-block-bg p-3 font-mono text-xs leading-5 text-code-block-text outline-none focus:ring-2 focus:ring-ring"
      />
      {mcpIsSample && (
        <p className="rounded-lg border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs leading-5 text-status-warning">
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
            <CheckStatus status={check.status} />
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
            {restartKeepsConversation
              ? "Applies the MCP configuration immediately: the agent restarts and resumes its session, so the conversation stays. Not while it works on a turn."
              : "Applies the MCP configuration immediately, but resets the agent's current conversation context."}
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
  );
}

// CheckStatus is the result of an MCP server's connectivity check.
function CheckStatus({status}: {status: string}) {
  const color =
    status === "running"
      ? "bg-status-warning/15 text-status-warning"
      : status === "completed"
        ? "bg-status-success/15 text-status-success"
        : status === "failed"
          ? "bg-destructive/15 text-destructive"
          : "bg-muted text-muted-foreground";
  return <span className={`rounded-full px-2 py-1 text-[11px] font-medium ${color}`}>{status}</span>;
}

function downloadJSON(filename: string, value: unknown) {
  downloadFile(filename, JSON.stringify(value, null, 2), "application/json");
}
