// Slash commands the chat handles itself for a native agent, which has no
// terminal UI for them: settings, compaction, a new conversation, the
// terminal and the login. Anything else is sent to the agent as typed (its
// own commands, e.g. Claude Code's /code-review, still work).

import type {AgentType} from "@/lib/agents";
import type {Features} from "./features";

export type SlashCommandKind = "model" | "effort" | "mode" | "plan" | "compact" | "clear" | "terminal" | "login" | "help";

export interface SlashCommandInfo {
  kind: SlashCommandKind;
  name: string;
  aliases?: string[];
  args?: string;
  description: string;
}

export const SLASH_COMMANDS: SlashCommandInfo[] = [
  {kind: "model", name: "model", args: "[model]", description: "Switch the model, or show the models"},
  {kind: "effort", name: "effort", aliases: ["think"], args: "[effort]", description: "Change the reasoning effort"},
  {kind: "mode", name: "mode", aliases: ["permissions"], args: "[bypass|auto|ask|plan]", description: "Change the permission mode"},
  {kind: "plan", name: "plan", description: "Plan first: switch to the plan mode"},
  {kind: "compact", name: "compact", description: "Summarize the conversation to free the context"},
  {kind: "clear", name: "clear", aliases: ["new", "reset"], description: "Start a new conversation"},
  {kind: "terminal", name: "terminal", aliases: ["shell"], description: "Open the terminal"},
  {kind: "login", name: "login", description: "Log the agent in, in the terminal"},
  {kind: "help", name: "help", description: "List these commands"},
];

export interface SlashCommand {
  info: SlashCommandInfo;
  // What follows the command, trimmed; empty when nothing does.
  arg: string;
}

// The command text names, or null when it isn't one of the chat's (it then
// goes to the agent). A command is the first word of a one-line message.
export function parseSlashCommand(text: string): SlashCommand | null {
  const trimmed = text.trim();
  if (!trimmed.startsWith("/") || trimmed.includes("\n")) return null;
  const [word, ...rest] = trimmed.slice(1).split(/\s+/);
  const name = word.toLowerCase();
  const info = SLASH_COMMANDS.find((command) => command.name === name || command.aliases?.includes(name));
  return info ? {info, arg: rest.join(" ")} : null;
}

// The commands that complete what is typed so far ("/mo" offers /model and
// /mode), while only the command word is typed: the one it names first,
// then the ones it starts, by name before alias.
export function matchingCommands(text: string): SlashCommandInfo[] {
  if (!text.startsWith("/") || /\s/.test(text)) return [];
  const prefix = text.slice(1).toLowerCase();
  const rank = (command: SlashCommandInfo) => {
    if (command.name === prefix) return 0;
    if (command.aliases?.includes(prefix)) return 1;
    if (command.name.startsWith(prefix)) return 2;
    if (command.aliases?.some((alias) => alias.startsWith(prefix))) return 3;
    return -1;
  };
  return SLASH_COMMANDS.map((command) => ({command, rank: rank(command)}))
    .filter(({rank}) => rank >= 0)
    .sort((a, b) => a.rank - b.rank)
    .map(({command}) => command);
}

// Whether the chat can run command for an agent with features, which the
// suggestions only offer then: the terminal and the login need the shell,
// the settings' commands need the settings.
export function runsHere(command: SlashCommandInfo, features: Pick<Features, "shell" | "settings">): boolean {
  switch (command.kind) {
    case "terminal":
    case "login":
      return features.shell;
    case "model":
    case "effort":
    case "mode":
    case "plan":
      return features.settings;
    case "compact":
    case "clear":
    case "help":
      return true;
  }
}

// What Tab puts in the composer for command: its name, and a space before
// its argument.
export function completion(command: SlashCommandInfo): string {
  return `/${command.name}${command.args ? " " : ""}`;
}

// Events that open a part of the page that isn't the composer's: the
// terminal (with a command to run in it), and a settings menu.
export const OPEN_TERMINAL_EVENT = "agentapi:open-terminal";
export const OPEN_SETTINGS_EVENT = "agentapi:open-settings";

export type SettingsMenu = "mode" | "model";

export function openTerminal(command?: string) {
  window.dispatchEvent(new CustomEvent<string | undefined>(OPEN_TERMINAL_EVENT, {detail: command}));
}

export function openSettings(menu: SettingsMenu) {
  window.dispatchEvent(new CustomEvent<SettingsMenu>(OPEN_SETTINGS_EVENT, {detail: menu}));
}

// The model or effort that arg picks from choices: its id or name, ignoring
// case, else the only one it starts.
export function pickChoice<T>(arg: string, choices: T[], keys: (choice: T) => string[]): T | undefined {
  const wanted = arg.trim().toLowerCase();
  if (!wanted) return undefined;
  const exact = choices.find((choice) => keys(choice).some((key) => key.toLowerCase() === wanted));
  if (exact) return exact;
  const prefixed = choices.filter((choice) => keys(choice).some((key) => key.toLowerCase().startsWith(wanted)));
  return prefixed.length === 1 ? prefixed[0] : undefined;
}

// The command that logs the agent in, in a terminal, when the agent isn't
// asking for one (then its login card says).
export function loginCommand(agentType: AgentType): string | undefined {
  switch (agentType) {
    case "claude":
      return "claude auth login";
    case "codex":
      return "codex login";
    case "pi":
      // Pi logs in from its own UI: /login once it runs.
      return "pi";
    default:
      return undefined;
  }
}
