"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Brain, ChevronDown, ClipboardList, Hand, LoaderCircle, Shield, ShieldCheck, Zap } from "lucide-react";
import { toast } from "sonner";
import {useChat} from "./chat-provider";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import type { AgentSettings, PermissionMode, SettingOptions } from "@/lib/chat-api";
import { getErrorMessage } from "@/lib/error-utils";
import { modeMeta, modelLabel, effortsOf } from "@/lib/session-settings";
import { OPEN_SETTINGS_EVENT, type SettingsMenu } from "@/lib/slash-commands";
import { cn } from "@/lib/utils";

const MODE_ICONS: Record<PermissionMode, ReactNode> = {
  bypass: <Zap />,
  auto: <ShieldCheck />,
  ask: <Hand />,
  plan: <ClipboardList />,
};

// The agent's permission mode, model and reasoning effort in the composer's
// toolbar (native agents with the settings capability). Each opens a menu
// that switches it; the values come from GET /settings when it opens.
export function SessionSettings({ className }: { className?: string }) {
  const { features, agentSettings, getSettings, updateSettings } = useChat();
  const [options, setOptions] = useState<SettingOptions | null>(null);
  // The menu that is open, also when a slash command opened it.
  const [openMenu, setOpenMenu] = useState<SettingsMenu | null>(null);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const enabled = features.settings && agentSettings !== null;

  // Only the latest load counts: an older one that ends later is stale.
  const loads = useRef(0);
  const load = useCallback(async () => {
    const seq = ++loads.current;
    setLoading(true);
    setLoadError("");
    try {
      const { options } = await getSettings();
      if (seq === loads.current) setOptions(options);
    } catch (error) {
      if (seq === loads.current) setLoadError(getErrorMessage(error));
    } finally {
      if (seq === loads.current) setLoading(false);
    }
  }, [getSettings]);

  // The names of the models (the settings carry ids) and whether the agent
  // has permission modes come with the choices.
  useEffect(() => {
    if (enabled) void load();
  }, [enabled, load]);

  // /model, /effort and /mode without a value open the menu.
  useEffect(() => {
    if (!enabled) return;
    const onOpen = (event: Event) => {
      setOpenMenu((event as CustomEvent<SettingsMenu>).detail);
      void load();
    };
    window.addEventListener(OPEN_SETTINGS_EVENT, onOpen);
    return () => window.removeEventListener(OPEN_SETTINGS_EVENT, onOpen);
  }, [enabled, load]);

  if (!enabled || !agentSettings) return null;

  // The choices can change with the agent (a restart, a new login), so
  // they are asked for again whenever a menu opens; the last ones show
  // meanwhile.
  const onOpenChange = (menu: SettingsMenu) => (open: boolean) => {
    setOpenMenu(open ? menu : null);
    if (open) void load();
  };

  const apply = async (change: AgentSettings) => {
    setSaving(true);
    try {
      setOptions((await updateSettings(change)).options);
    } catch (error) {
      toast.error("Couldn't change the setting", { description: getErrorMessage(error) });
    } finally {
      setSaving(false);
    }
  };

  const mode = agentSettings.permission_mode;
  const meta = modeMeta(mode);
  // Pi has no permission modes: its trigger shows once the options say so.
  const showMode = mode !== undefined || (options?.permission_modes.length ?? 0) > 0;
  const efforts = effortsOf(options, agentSettings.model);
  const status = (
    <>
      {loading && !options && <DropdownMenuItem disabled><LoaderCircle className="animate-spin" />Loading…</DropdownMenuItem>}
      {loadError && (
        <DropdownMenuItem onSelect={(event) => { event.preventDefault(); void load(); }} className="text-destructive">
          Couldn&apos;t load the choices: {loadError}. Tap to try again.
        </DropdownMenuItem>
      )}
    </>
  );

  return (
    <div className={cn("flex min-w-0 items-center gap-1", className)} data-session-settings>
      {showMode && (
        <DropdownMenu open={openMenu === "mode"} onOpenChange={onOpenChange("mode")}>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={saving}
              className="h-9 min-w-0 shrink-0 gap-1 rounded-full px-2.5 text-xs text-muted-foreground hover:text-foreground"
              title={`Permission mode: ${meta.label}. ${meta.description}`}
            >
              {saving ? <LoaderCircle className="animate-spin" /> : mode ? MODE_ICONS[mode] : <Shield />}
              <span className="max-[380px]:sr-only">{meta.label}</span>
              <ChevronDown className="size-3 opacity-60" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" side="top" className="max-h-[70dvh] w-72 overflow-y-auto">
            <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Permission mode</DropdownMenuLabel>
            {status}
            {options && (
              <DropdownMenuRadioGroup value={mode ?? ""} onValueChange={(value) => void apply({ permission_mode: value as PermissionMode })}>
                {options.permission_modes.map((value) => {
                  const item = modeMeta(value);
                  return (
                    <DropdownMenuRadioItem key={value} value={value} className="min-h-11 items-start">
                      <span className="grid">
                        <span className="flex items-center gap-1.5">{MODE_ICONS[value]}{item.label}</span>
                        <span className="text-[11px] text-muted-foreground">{item.description}</span>
                      </span>
                    </DropdownMenuRadioItem>
                  );
                })}
              </DropdownMenuRadioGroup>
            )}
            {options && !mode && (
              <p className="px-2 py-1.5 text-[11px] text-muted-foreground">
                The agent runs in a mode of its own; pick one to switch.
              </p>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}

      {agentSettings.model && (
        <DropdownMenu open={openMenu === "model"} onOpenChange={onOpenChange("model")}>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={saving}
              className="h-9 min-w-0 shrink gap-1 rounded-full px-2.5 text-xs text-muted-foreground hover:text-foreground"
              title="Model and reasoning effort"
            >
              <Brain />
              <span className="min-w-0 truncate">
                {modelLabel(options, agentSettings.model)}
                {agentSettings.effort && <span className="opacity-70"> · {agentSettings.effort}</span>}
              </span>
              <ChevronDown className="size-3 shrink-0 opacity-60" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" side="top" className="max-h-[70dvh] w-72 overflow-y-auto">
            <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Model</DropdownMenuLabel>
            {status}
            {options && (
              <DropdownMenuRadioGroup value={agentSettings.model} onValueChange={(value) => void apply({ model: value })}>
                {options.models.map((model) => (
                  <DropdownMenuRadioItem key={model.id} value={model.id} className="min-h-11 items-start">
                    <span className="grid min-w-0">
                      <span className="truncate">{model.name || model.id}</span>
                      {model.description && <span className="text-[11px] text-muted-foreground">{model.description}</span>}
                    </span>
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            )}
            {efforts.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Reasoning effort</DropdownMenuLabel>
                <DropdownMenuRadioGroup value={agentSettings.effort ?? ""} onValueChange={(value) => void apply({ effort: value })}>
                  {efforts.map((effort) => (
                    <DropdownMenuRadioItem key={effort} value={effort} className="min-h-10">
                      {effort}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
