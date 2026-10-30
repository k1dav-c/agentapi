import type { PermissionMode, SettingOptions } from "./chat-api";

export interface ModeMeta {
  label: string;
  description: string;
}

const MODES: Record<PermissionMode, ModeMeta> = {
  bypass: { label: "Bypass", description: "Tools run without asking." },
  auto: { label: "Auto", description: "The agent's own review decides what may run." },
  ask: { label: "Ask", description: "Tool calls that need approval come here first." },
  plan: { label: "Plan", description: "The agent proposes a plan before it changes anything." },
};

// How to show a permission mode; a missing one is a mode of the agent's own
// (e.g. Claude Code's acceptEdits after an approved plan), and one a newer
// server added shows by its name.
export function modeMeta(mode: PermissionMode | undefined): ModeMeta {
  if (!mode) return { label: "Custom", description: "The agent runs in a mode of its own." };
  return MODES[mode] ?? { label: mode, description: "A mode this chat doesn't know." };
}

// The name of model id, or the id itself until the options are known or
// for a model that isn't one of them (one the command line chose).
export function modelLabel(options: SettingOptions | null, id: string): string {
  return options?.models.find((model) => model.id === id)?.name || id;
}

// The reasoning efforts of model id; none when it has no choice of effort
// or isn't one of the options.
export function effortsOf(options: SettingOptions | null, id: string | undefined): string[] {
  if (!id) return [];
  return options?.models.find((model) => model.id === id)?.efforts ?? [];
}
