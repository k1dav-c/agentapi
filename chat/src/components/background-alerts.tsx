"use client";

import { useEffect, useRef, useState } from "react";
import { AgentType, useChat } from "./chat-provider";
import { getAgentState } from "@/lib/session-status";
import { describeTerminalPrompt } from "@/lib/terminal-option";
import { alertForTransition, faviconDataUrl, notificationsEnabled, type FaviconBadge } from "@/lib/background-alerts";

// Lets people who aren't looking at the tab know what the agent is doing:
// a state dot on the tab icon, and (if they turned it on) a browser
// notification when the agent needs them or finishes while the tab is in
// the background.
export function BackgroundAlerts() {
  const { serverStatus, connectionStatus, terminalPrompt, agentType, messages } = useChat();
  const kind = getAgentState(serverStatus, connectionStatus, terminalPrompt);
  const previous = useRef(kind);
  // A task finished while the tab was hidden and hasn't been seen yet.
  const [unseenFinish, setUnseenFinish] = useState(false);
  const name = agentType === "unknown" ? "The agent" : AgentType[agentType].displayName;
  const task = messages.findLast((message) => message.role === "user")?.content ?? "";

  useEffect(() => {
    const onVisible = () => {
      if (!document.hidden) setUnseenFinish(false);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, []);

  useEffect(() => {
    const alert = alertForTransition(previous.current, kind);
    previous.current = kind;
    if (!alert || !document.hidden) return;
    if (alert === "finished") setUnseenFinish(true);
    if (!notificationsEnabled() || typeof Notification === "undefined" || Notification.permission !== "granted") return;
    const notification = new Notification(alert === "needs-you" ? `${name} needs you` : `${name} finished`, {
      body: alert === "needs-you" ? describeTerminalPrompt(terminalPrompt).title : task.replace(/\s+/g, " ").slice(0, 120),
      tag: `agentapi-${alert}`,
    });
    notification.onclick = () => {
      window.focus();
      notification.close();
    };
    // Only state transitions should notify; name, task and prompt are read
    // at that moment.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind]);

  useEffect(() => {
    const badge: FaviconBadge =
      kind === "needs-you" ? "needs-you" : kind === "working" ? "working" : unseenFinish ? "finished" : null;
    let link = document.querySelector<HTMLLinkElement>("link[data-agentapi-icon]");
    if (!link) {
      // Replace the static icon so browsers don't prefer it.
      document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]').forEach((icon) => icon.remove());
      link = document.createElement("link");
      link.rel = "icon";
      link.type = "image/svg+xml";
      link.setAttribute("data-agentapi-icon", "");
      document.head.appendChild(link);
    }
    link.href = faviconDataUrl(badge, agentType);
  }, [kind, unseenFinish, agentType]);

  return null;
}
