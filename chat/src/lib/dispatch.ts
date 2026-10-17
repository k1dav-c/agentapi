// How a message is delivered while the agent works (POST /message
// dispatch_mode), and what the composer offers for it.

import { isImeComposing, type ImeKeyEvent } from "./keys";

export type DispatchMode = "queue" | "steer" | "interrupt";
export type Transport = "pty" | "acp" | "native";

// The transport a status_change event, GET /status or the cache reports, or
// null for anything else (a server too old to report it).
export function parseTransport(value: unknown): Transport | null {
  return value === "pty" || value === "acp" || value === "native" ? value : null;
}

export interface Capabilities {
  steer: boolean;
  interrupt: boolean;
  resume: boolean;
  images: boolean;
  // The permission mode, model and effort can change (GET/PUT /settings).
  // Missing from servers that predate it.
  settings?: boolean;
}

export interface DispatchContext {
  transport: Transport;
  // Optional operations of a native agent; null for other transports and
  // while the agent starts, when there is no turn to steer or stop.
  capabilities: Capabilities | null;
  serverStatus: string;
  // Requests the agent waits on; the turn is still open while it waits.
  pendingRequestCount: number;
}

// Whether the agent is in the middle of a turn: working, or waiting on an
// answer to a request it made during the turn.
export function turnActive(context: DispatchContext): boolean {
  return context.serverStatus === "running" || (context.transport === "native" && context.pendingRequestCount > 0);
}

// Whether the running turn can be stopped with POST /interrupt.
export function canInterrupt(context: DispatchContext): boolean {
  return context.transport === "native" && Boolean(context.capabilities?.interrupt) && turnActive(context);
}

// Whether Stop can stop what the agent is doing: Esc on the terminal while
// a PTY agent works, POST /interrupt on the native transport. ACP has
// neither.
export function canStop(context: DispatchContext): boolean {
  return context.transport === "pty" ? context.serverStatus === "running" : canInterrupt(context);
}

// Whether a message can be added to the turn the agent is working on. Not
// while it waits on a request: answer that first.
export function canSteer(context: DispatchContext): boolean {
  return context.transport === "native" && Boolean(context.capabilities?.steer) && context.serverStatus === "running";
}

// The ways to send a message now, the default first.
export function dispatchModes(context: DispatchContext): DispatchMode[] {
  const modes: DispatchMode[] = ["queue"];
  if (canSteer(context)) modes.push("steer");
  if (canInterrupt(context)) modes.push("interrupt");
  return modes;
}

// The mode an Enter key press sends with: Enter queues, ⌘/Ctrl+Enter
// steers and ⌘/Ctrl+Shift+Enter interrupts and sends, when available.
// null for keys that don't send (Shift+Enter is a new line, and an Enter
// that picks an IME candidate only ends the composition).
export function dispatchModeForKey(
  event: {key: string; shiftKey: boolean; metaKey: boolean; ctrlKey: boolean} & ImeKeyEvent,
  modes: DispatchMode[],
): DispatchMode | null {
  if (event.key !== "Enter" || isImeComposing(event)) return null;
  const mod = event.metaKey || event.ctrlKey;
  if (event.shiftKey && !mod) return null;
  if (mod && event.shiftKey) return modes.includes("interrupt") ? "interrupt" : "queue";
  if (mod) return modes.includes("steer") ? "steer" : "queue";
  return "queue";
}

// What to tell the user once a message was accepted, or null when it went
// out as a new turn.
export function sendNotice(result: {queued: boolean; steered?: boolean}, mode: DispatchMode): string | null {
  if (result.steered) return "Added to the current turn";
  if (!result.queued) return null;
  return mode === "interrupt" ? "Stopping the agent; your message goes next" : "Task queued";
}

// The queue order after moving the message id by delta places, or null if
// it can't move that way.
export function moveInQueue(ids: number[], id: number, delta: number): number[] | null {
  const from = ids.indexOf(id);
  const to = from + delta;
  if (from < 0 || to < 0 || to >= ids.length || delta === 0) return null;
  const next = ids.slice();
  next.splice(from, 1);
  next.splice(to, 0, id);
  return next;
}
