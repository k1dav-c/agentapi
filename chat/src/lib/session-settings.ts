import type { ModelOption, PermissionMode, ProviderHealth, SettingOptions } from "./chat-api";

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

// The provider setting of an agent on its own provider.
export const DEFAULT_PROVIDER = "default";

// A group of the model menu: the agent's own models, then each provider's.
export interface ModelGroup {
  provider: string;
  // Empty for the agent's own.
  label: string;
  host?: string;
  origin?: string;
  // How the provider answered over the last hour, once the agent called it.
  health?: ProviderHealth;
  models: ModelOption[];
}

// The models of provider (the agent's own for none or default).
function modelsOf(options: SettingOptions | null, provider: string | undefined): ModelOption[] {
  if (!provider || provider === DEFAULT_PROVIDER) return options?.models ?? [];
  return options?.providers?.find((p) => p.id === provider)?.models ?? [];
}

// The name of model id on provider, or the id itself until the options are
// known or for a model that isn't one of them (one the command line chose).
export function modelLabel(options: SettingOptions | null, id: string, provider?: string): string {
  return modelsOf(options, provider).find((model) => model.id === id)?.name || id;
}

// The name of provider, for one other than the agent's own.
export function providerLabel(options: SettingOptions | null, provider: string | undefined): string {
  if (!provider || provider === DEFAULT_PROVIDER) return "";
  return options?.providers?.find((p) => p.id === provider)?.name || provider;
}

// The reasoning efforts of model id on provider; none when it has no
// choice of effort or isn't one of the options.
export function effortsOf(options: SettingOptions | null, id: string | undefined, provider?: string): string[] {
  if (!id) return [];
  return modelsOf(options, provider).find((model) => model.id === id)?.efforts ?? [];
}

// The groups of the model menu: the agent's own models, then each
// provider's that offers some (a provider that lists none can't be picked
// from a menu).
export function modelGroups(options: SettingOptions | null): ModelGroup[] {
  if (!options) return [];
  const groups: ModelGroup[] = [{ provider: DEFAULT_PROVIDER, label: "", models: options.models }];
  for (const p of options.providers ?? []) {
    if (p.models.length > 0) groups.push({ provider: p.id, label: p.name || p.id, host: p.host, origin: p.origin, health: p.health, models: p.models });
  }
  return groups;
}

// A model menu item's value: the provider and the model, which the radio
// group keeps as one string.
export function modelValue(provider: string | undefined, model: string): string {
  return `${provider || DEFAULT_PROVIDER}\n${model}`;
}

// The change a model menu item makes from current: the model, and the
// provider when it is another.
export function modelChange(value: string, currentProvider: string | undefined): { provider?: string; model: string } {
  const [provider, model] = value.split("\n", 2) as [string, string];
  if (provider === (currentProvider || DEFAULT_PROVIDER)) return { model };
  return { provider, model };
}

// providerSwitchHint says, in a line, what switching providers does with
// the agent: Claude Code switches at once and starts again to go back to
// its own; Codex starts a new thread with the conversation.
export function providerSwitchHint(agentType: string): string {
  switch (agentType) {
    case "codex":
      return "Switching provider starts a new Codex thread with this conversation.";
    case "claude":
      return "Switches at once; back to Anthropic, Claude Code restarts on this session.";
    default:
      return "Switching provider keeps this conversation.";
  }
}
