import type {InstanceState} from "./hub";

// The hub's page and the chat of the instance it opens in a frame talk with
// window messages. The chat draws the bar above itself (one bar, with its
// own actions in it, instead of the hub's bar and a row of the chat's), the
// hub tells it what to show there, and the chat asks the hub to go back.

// What the hub tells the chat about the instance.
export type FrameInstance = {
  name: string;
  state: InstanceState;
  label: string;
  agentType?: string;
  agent?: string;
  hostname?: string;
  // The chat without the hub, for a tab of its own.
  tab: string;
};

export type ToFrame = {source: "agentapi.hub"; type: "instance"; instance: FrameInstance};
export type FromFrame = {source: "agentapi.hub"; type: "ready" | "back"};

const SOURCE = "agentapi.hub";

export function toFrame(instance: FrameInstance): ToFrame {
  return {source: SOURCE, type: "instance", instance};
}

export function fromFrame(type: FromFrame["type"]): FromFrame {
  return {source: SOURCE, type};
}

// A message from a page of the same origin, in the protocol; the hub and
// the chat it frames are served by the same hub.
export function frameMessage<T extends ToFrame | FromFrame>(event: MessageEvent): T | undefined {
  if (event.origin !== window.location.origin) return undefined;
  const data = event.data as {source?: unknown} | null;
  return data && typeof data === "object" && data.source === SOURCE ? (data as T) : undefined;
}
