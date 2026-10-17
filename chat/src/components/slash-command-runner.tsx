"use client";

import {toast} from "sonner";
import {useChat} from "./chat-provider";
import type {PermissionMode} from "@/lib/chat-api";
import {getErrorMessage} from "@/lib/error-utils";
import {SLASH_COMMANDS, loginCommand, pickChoice, type SlashCommand} from "@/lib/slash-commands";

// Events the parts of the page that own a menu or a view listen to.
export const OPEN_TERMINAL_EVENT = "agentapi:open-terminal";
export const OPEN_SETTINGS_EVENT = "agentapi:open-settings";

export type SettingsMenu = "mode" | "model";

export function openTerminal(command?: string) {
  window.dispatchEvent(new CustomEvent<string | undefined>(OPEN_TERMINAL_EVENT, {detail: command}));
}

export function openSettings(menu: SettingsMenu) {
  window.dispatchEvent(new CustomEvent<SettingsMenu>(OPEN_SETTINGS_EVENT, {detail: menu}));
}

// Runs the chat's slash commands (lib/slash-commands) for a native agent.
// run resolves to whether the command was handled here; one that wasn't
// goes to the agent as typed.
export function useSlashCommandRunner() {
  const {capabilities, agentSettings, getSettings, updateSettings, compact, deleteMessages, terminalAvailable, loginRequired, agentType} = useChat();

  const change = async (label: string, run: () => Promise<unknown>) => {
    try {
      await run();
      toast.success(label);
    } catch (error) {
      toast.error("Couldn't run the command", {description: getErrorMessage(error)});
    }
  };

  // The settings' choices, or null (said) when the agent has none.
  const options = async () => {
    if (!capabilities?.settings || !agentSettings) {
      toast.error("This agent's settings can't be changed");
      return null;
    }
    try {
      return (await getSettings()).options;
    } catch (error) {
      toast.error("Couldn't load the agent's settings", {description: getErrorMessage(error)});
      return null;
    }
  };

  const run = async ({info, arg}: SlashCommand): Promise<boolean> => {
    switch (info.kind) {
      case "model": {
        if (!arg) {
          openSettings("model");
          return true;
        }
        const choices = await options();
        if (!choices) return true;
        const model = pickChoice(arg, choices.models, (m) => [m.id, m.name]);
        if (!model) {
          toast.error(`No model named "${arg}"`, {description: `Models: ${choices.models.map((m) => m.name || m.id).join(", ")}`});
          return true;
        }
        await change(`Model: ${model.name || model.id}`, () => updateSettings({model: model.id}));
        return true;
      }
      case "effort": {
        if (!arg) {
          openSettings("model");
          return true;
        }
        const choices = await options();
        if (!choices) return true;
        const efforts = choices.models.find((m) => m.id === agentSettings?.model)?.efforts ?? [];
        const effort = pickChoice(arg, efforts, (e) => [e]);
        if (!effort) {
          toast.error(`No reasoning effort "${arg}"`, {description: efforts.length ? `Efforts: ${efforts.join(", ")}` : "The model has no choice of effort."});
          return true;
        }
        await change(`Reasoning effort: ${effort}`, () => updateSettings({effort}));
        return true;
      }
      case "mode":
      case "plan": {
        if (info.kind === "mode" && !arg) {
          openSettings("mode");
          return true;
        }
        const choices = await options();
        if (!choices) return true;
        const mode = info.kind === "plan" ? choices.permission_modes.find((m) => m === "plan") : pickChoice(arg, choices.permission_modes, (m) => [m]);
        if (!mode) {
          toast.error(info.kind === "plan" ? "This agent has no plan mode" : `No permission mode "${arg}"`, {
            description: choices.permission_modes.length ? `Modes: ${choices.permission_modes.join(", ")}` : "The agent has no permission modes.",
          });
          return true;
        }
        await change(`Permission mode: ${mode}`, () => updateSettings({permission_mode: mode as PermissionMode}));
        return true;
      }
      case "compact":
        // Claude Code compacts with the message itself.
        if (!capabilities?.compact) return false;
        await change("Context compacted", compact);
        return true;
      case "clear":
        await change("Started a new conversation", deleteMessages);
        return true;
      case "terminal":
      case "login": {
        if (!terminalAvailable) {
          toast.error("There is no terminal", {description: "It needs the native transport, and --terminal."});
          return true;
        }
        openTerminal(info.kind === "login" ? (loginRequired?.command ?? loginCommand(agentType)) : undefined);
        return true;
      }
      case "help":
        toast.info("Chat commands", {
          description: (
            <ul className="grid gap-0.5">
              {SLASH_COMMANDS.map((c) => (
                <li key={c.name}>
                  <code>/{c.name}{c.args ? ` ${c.args}` : ""}</code> {c.description}
                </li>
              ))}
              <li>Anything else goes to the agent.</li>
            </ul>
          ),
          duration: 15000,
        });
        return true;
    }
  };

  return run;
}
