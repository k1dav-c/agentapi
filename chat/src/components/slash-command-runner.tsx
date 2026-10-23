"use client";

import {toast} from "sonner";
import {useChatActions, useChatSelector} from "./chat-provider";
import type {PermissionMode} from "@/lib/chat-api";
import {getErrorMessage} from "@/lib/error-utils";
import {effortsOf, modeMeta} from "@/lib/session-settings";
import {SLASH_COMMANDS, loginCommand, openSettings, openTerminal, pickChoice, type SlashCommand} from "@/lib/slash-commands";

// Runs the chat's slash commands (lib/slash-commands) for a native agent.
// handles says at once whether the chat runs a command (Claude Code
// compacts with the message itself, so /compact goes to it); run runs it,
// saying how it went in a toast.
export function useSlashCommandRunner() {
  const capabilities = useChatSelector((chat) => chat.capabilities);
  const features = useChatSelector((chat) => chat.features);
  const agentSettings = useChatSelector((chat) => chat.agentSettings);
  const loginRequired = useChatSelector((chat) => chat.loginRequired);
  const agentType = useChatSelector((chat) => chat.agentType);
  const {getSettings, updateSettings, compact, deleteMessages} = useChatActions();

  const handles = ({info}: SlashCommand) => info.kind !== "compact" || Boolean(capabilities?.compact);

  // Runs a change, with a toast that says it is under way, then how it went.
  const change = async (pending: string, done: string, run: () => Promise<unknown>) => {
    const id = toast.loading(pending);
    try {
      await run();
      toast.success(done, {id});
    } catch (error) {
      toast.error("Couldn't run the command", {id, description: getErrorMessage(error)});
    }
  };

  // Whether the agent's settings can change; says so if not.
  const configurable = () => {
    if (features.settings && agentSettings) return true;
    toast.error("This agent's settings can't be changed");
    return false;
  };

  // The settings' choices, or null (said) when they can't be had.
  const options = async () => {
    if (!configurable()) return null;
    try {
      return (await getSettings()).options;
    } catch (error) {
      toast.error("Couldn't load the agent's settings", {description: getErrorMessage(error)});
      return null;
    }
  };

  const run = async ({info, arg}: SlashCommand): Promise<void> => {
    switch (info.kind) {
      case "model": {
        if (!arg) {
          if (configurable()) openSettings("model");
          return;
        }
        const choices = await options();
        if (!choices) return;
        const model = pickChoice(arg, choices.models, (m) => [m.id, m.name]);
        if (!model) {
          toast.error(`No model named "${arg}"`, {description: `Models: ${choices.models.map((m) => m.name || m.id).join(", ")}`});
          return;
        }
        const name = model.name || model.id;
        await change(`Switching to ${name}…`, `Model: ${name}`, () => updateSettings({model: model.id}));
        return;
      }
      case "effort": {
        if (!arg) {
          if (configurable()) openSettings("model");
          return;
        }
        const choices = await options();
        if (!choices) return;
        const efforts = effortsOf(choices, agentSettings?.model);
        const effort = pickChoice(arg, efforts, (e) => [e]);
        if (!effort) {
          toast.error(`No reasoning effort "${arg}"`, {description: efforts.length ? `Efforts: ${efforts.join(", ")}` : "The model has no choice of effort."});
          return;
        }
        await change(`Changing the effort to ${effort}…`, `Reasoning effort: ${effort}`, () => updateSettings({effort}));
        return;
      }
      case "mode":
      case "plan": {
        if (info.kind === "mode" && !arg) {
          if (configurable()) openSettings("mode");
          return;
        }
        const choices = await options();
        if (!choices) return;
        const mode = info.kind === "plan" ? choices.permission_modes.find((m) => m === "plan") : pickChoice(arg, choices.permission_modes, (m) => [m]);
        if (!mode) {
          toast.error(info.kind === "plan" ? "This agent has no plan mode" : `No permission mode "${arg}"`, {
            description: choices.permission_modes.length ? `Modes: ${choices.permission_modes.join(", ")}` : "The agent has no permission modes.",
          });
          return;
        }
        const label = modeMeta(mode as PermissionMode).label;
        await change(`Switching to ${label}…`, `Permission mode: ${label}`, () => updateSettings({permission_mode: mode as PermissionMode}));
        return;
      }
      case "compact":
        // Codex compacts in a turn of its own, after the request returns.
        await change("Compacting the context…", agentType === "codex" ? "Codex is compacting the context" : "Context compacted", compact);
        return;
      case "clear":
        await change("Starting a new conversation…", "Started a new conversation", deleteMessages);
        return;
      case "terminal":
      case "login":
        if (!features.shell) {
          toast.error("There is no terminal", {description: "It needs the native transport, and --terminal."});
          return;
        }
        openTerminal(info.kind === "login" ? (loginRequired?.command ?? loginCommand(agentType)) : undefined);
        return;
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
        return;
    }
  };

  return {handles, run};
}
