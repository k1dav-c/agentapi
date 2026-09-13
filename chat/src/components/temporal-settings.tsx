"use client";

import {useEffect, useRef, useState} from "react";
import {Download, LoaderCircle, Save, Trash2, Upload} from "lucide-react";
import {toast} from "sonner";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import type {TemporalConfig} from "@/lib/temporal-config";

const blankConfig: TemporalConfig = {
  agent_url: "http://localhost:3284",
  temporal_address: "localhost:7233",
  namespace: "default",
  task_queue: "",
  temporal_tls: false,
  channel_id: "",
  allowed_users: [],
  agent_token_env: "AGENTAPI_API_TOKEN",
  temporal_api_key_env: "TEMPORAL_API_KEY",
  bot_token_env: "DISCORD_BOT_TOKEN",
};

export function TemporalSettings() {
  const {getTemporal, updateTemporal, getTemporalProfiles, saveTemporalProfile, importTemporalProfiles, deleteTemporalProfile, applyTemporalProfile} = useChat();
  const [config, setConfig] = useState<TemporalConfig>(blankConfig);
  const [profiles, setProfiles] = useState<Record<string, TemporalConfig>>({});
  const [path, setPath] = useState("");
  const [profileName, setProfileName] = useState("");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);

  const load = async () => {
    setLoading(true);
    try {
      const [current, saved] = await Promise.all([getTemporal(), getTemporalProfiles()]);
      setConfig(current.config);
      setPath(current.path);
      setProfiles(saved.profiles);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not load Temporal settings");
    } finally { setLoading(false); }
  };
  // The tab is mounted only when the Explorer opens; load once per mount.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void load(); }, []);

  const update = <K extends keyof TemporalConfig>(key: K, value: TemporalConfig[K]) => setConfig((previous) => ({...previous, [key]: value}));
  const save = async () => {
    setSaving(true);
    try {
      const result = await updateTemporal({...config, allowed_users: config.allowed_users.filter(Boolean)});
      setConfig(result.config); setPath(result.path);
      toast.success("Temporal settings saved");
    } catch (error) { toast.error(error instanceof Error ? error.message : "Could not save Temporal settings"); }
    finally { setSaving(false); }
  };
  const saveProfile = async () => {
    const name = profileName.trim();
    if (!name) { toast.error("Enter a profile name"); return; }
    try { const result = await saveTemporalProfile(name, config); setProfiles(result.profiles); setProfileName(""); toast.success(`Profile “${name}” saved`); }
    catch (error) { toast.error(error instanceof Error ? error.message : "Could not save profile"); }
  };
  const applyProfile = async (name: string) => {
    try { const result = await applyTemporalProfile(name); setConfig(result.config); setPath(result.path); toast.success(`Profile “${name}” applied`); }
    catch (error) { toast.error(error instanceof Error ? error.message : "Could not apply profile"); }
  };
  const exportProfiles = () => {
    const blob = new Blob([JSON.stringify({profiles}, null, 2)], {type: "application/json"});
    const link = document.createElement("a"); link.href = URL.createObjectURL(blob); link.download = "agentapi-temporal-profiles.json"; link.click(); URL.revokeObjectURL(link.href);
  };
  const exportConfig = () => {
    const blob = new Blob([JSON.stringify({config}, null, 2)], {type: "application/json"});
    const link = document.createElement("a"); link.href = URL.createObjectURL(blob); link.download = "agentapi-temporal.json"; link.click(); URL.revokeObjectURL(link.href);
  };
  const importProfiles = async (file: File) => {
    try {
      const value = JSON.parse(await file.text()) as {profiles?: Record<string, TemporalConfig>};
      if (!value.profiles || typeof value.profiles !== "object") throw new Error("invalid profiles");
      const result = await importTemporalProfiles(value.profiles); setProfiles(result.profiles); toast.success("Temporal profiles imported");
    } catch (error) { toast.error(error instanceof Error ? error.message : "Invalid Temporal profile export"); }
  };
  const removeProfile = async (name: string) => {
    try { const result = await deleteTemporalProfile(name); setProfiles(result.profiles); }
    catch (error) { toast.error(error instanceof Error ? error.message : "Could not delete profile"); }
  };
  if (loading) return <div className="flex items-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />Loading Temporal settings…</div>;
  return <div className="space-y-4">
    <div><h3 className="text-sm font-medium">Temporal handoff</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">Settings are saved in the project and used by the <code>agentapi discord</code> worker. Environment variables override stored secrets when present.</p></div>
    <div className="grid grid-cols-2 gap-3">
      <label className="space-y-1.5"><span className="text-xs font-medium">AgentAPI URL</span><input value={config.agent_url} onChange={(e) => update("agent_url", e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
      <label className="space-y-1.5"><span className="text-xs font-medium">Temporal address</span><input value={config.temporal_address} onChange={(e) => update("temporal_address", e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
      <label className="space-y-1.5"><span className="text-xs font-medium">Namespace</span><input value={config.namespace} onChange={(e) => update("namespace", e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
      <label className="space-y-1.5"><span className="text-xs font-medium">Task queue</span><input value={config.task_queue} onChange={(e) => update("task_queue", e.target.value)} placeholder="Auto-derived" className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
      <label className="space-y-1.5"><span className="text-xs font-medium">Discord channel ID</span><input value={config.channel_id} onChange={(e) => update("channel_id", e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
      <label className="space-y-1.5"><span className="text-xs font-medium">Allowed user IDs</span><input value={config.allowed_users.join(", ")} onChange={(e) => update("allowed_users", e.target.value.split(",").map((v) => v.trim()).filter(Boolean))} className="h-9 w-full rounded-md border bg-background px-2 text-sm" /></label>
    </div>
    <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={config.temporal_tls} onChange={(e) => update("temporal_tls", e.target.checked)} />Use TLS for Temporal</label>
    <div className="grid grid-cols-3 gap-3">
      {([["agent_token_env", "Agent token env"], ["temporal_api_key_env", "Temporal API key env"], ["bot_token_env", "Discord bot token env"]] as const).map(([key, label]) => <label key={key} className="space-y-1.5"><span className="text-xs font-medium">{label}</span><input value={config[key]} onChange={(e) => update(key, e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-xs" /></label>)}
    </div>
    <div className="grid grid-cols-3 gap-3">
      {([["agent_token", "AgentAPI token"], ["temporal_api_key", "Temporal API key"], ["bot_token", "Discord bot token"]] as const).map(([key, label]) => <label key={key} className="space-y-1.5"><span className="text-xs font-medium">{label}</span><input type="password" value={config[key] ?? ""} onChange={(e) => update(key, e.target.value)} className="h-9 w-full rounded-md border bg-background px-2 text-xs" /></label>)}
    </div>
    <div className="flex items-center justify-between gap-2"><span className="truncate text-[11px] text-muted-foreground" title={path}>{path}</span><div className="flex gap-1"><Button type="button" size="icon" variant="ghost" title="Export current settings" onClick={exportConfig}><Download /></Button><Button type="button" size="icon" variant="ghost" title="Import profiles" onClick={() => importRef.current?.click()}><Upload /></Button><input ref={importRef} type="file" accept="application/json,.json" className="hidden" onChange={(e) => { const file = e.target.files?.[0]; if (file) void importProfiles(file); e.target.value = ""; }} /><Button type="button" size="icon" variant="ghost" title="Export profiles" onClick={exportProfiles}><Download /></Button><Button type="button" onClick={() => void save()} disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}Save</Button></div></div>
    <div className="space-y-2 rounded-xl border p-3"><p className="text-sm font-medium">Profiles</p><div className="flex gap-2"><input value={profileName} onChange={(e) => setProfileName(e.target.value)} placeholder="Profile name" className="min-w-0 flex-1 rounded-md border bg-background px-3 text-sm" /><Button type="button" size="sm" variant="outline" onClick={() => void saveProfile()}><Save />Save</Button></div>{Object.keys(profiles).sort().map((name) => <div key={name} className="flex items-center justify-between rounded-lg bg-muted/40 px-3 py-2 text-sm"><span>{name}</span><span className="flex gap-1"><Button type="button" size="sm" variant="ghost" onClick={() => void applyProfile(name)}>Apply</Button><Button type="button" size="icon" variant="ghost" title={`Delete ${name}`} onClick={() => void removeProfile(name)}><Trash2 /></Button></span></div>)}</div>
  </div>;
}
