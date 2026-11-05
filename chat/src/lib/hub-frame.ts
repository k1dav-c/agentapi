import {displayName, instanceState, instanceVersion, stateLabel, type HubInstance, type InstanceState, type InstanceUpdate} from "./hub";
import {shownUpdate} from "./hub-update";

// The hub's page and the chat of the instance it opens in a frame talk with
// window messages. The chat draws the bar above itself (one bar, with its
// own actions in it, instead of the hub's bar and a row of the chat's), the
// hub tells it what to show there, and the chat asks the hub to go back, to
// cancel the instance's update, or to open another agent of its machine.

// What the hub tells the chat about the instance.
export type FrameInstance = {
  id: string;
  name: string;
  state: InstanceState;
  label: string;
  agentType?: string;
  agent?: string;
  hostname?: string;
  // The AgentAPI version it runs, and the hub's, which marks an older one;
  // lastKnown: the hub doesn't follow it now, so it is the one it ran last.
  version?: string;
  hubVersion?: string;
  lastKnown?: boolean;
  // An update under way, or one that failed, for its chip.
  update?: InstanceUpdate;
  // The chat without the hub, for a tab of its own.
  tab: string;
  // The agents of its machine, itself among them, for the bar's switch;
  // none when it is the only one there. A chat from before this release
  // ignores them.
  machine?: MachineAgent[];
};

// An agent of the open instance's machine, as the bar's switch shows it.
export type MachineAgent = {
  id: string;
  name: string;
  state: InstanceState;
  label: string;
  agentType?: string;
  agent?: string;
};

export type ToFrame = {source: "agentapi.hub"; type: "instance"; instance: FrameInstance};
export type FromFrame = {source: "agentapi.hub"; type: "ready" | "back" | "cancel-update"} | {source: "agentapi.hub"; type: "open"; id: string};

const SOURCE = "agentapi.hub";

export function toFrame(instance: FrameInstance): ToFrame {
  return {source: SOURCE, type: "instance", instance};
}

export function fromFrame(type: "ready" | "back" | "cancel-update"): FromFrame {
  return {source: SOURCE, type};
}

// openFromFrame asks the hub to open the agent id instead.
export function openFromFrame(id: string): FromFrame {
  return {source: SOURCE, type: "open", id};
}

// frameInstance is what the hub tells the chat of instance: agent is the
// agent's name, tab the chat's address without the hub.
export function frameInstance(
  instance: HubInstance,
  {hubVersion, agent, tab, machine}: {hubVersion: string; agent: string; tab: string; machine?: MachineAgent[]},
): FrameInstance {
  return {
    id: instance.id,
    name: displayName(instance),
    state: instanceState(instance),
    label: stateLabel(instance),
    agentType: instance.agent_type || undefined,
    agent: agent || undefined,
    hostname: instance.hostname || undefined,
    version: instanceVersion(instance) || undefined,
    hubVersion: hubVersion || undefined,
    lastKnown: instance.connection !== "connected",
    update: shownUpdate(instance),
    tab,
    ...(machine && machine.length > 1 ? {machine} : {}),
  };
}

// A message from a page of the same origin, in the protocol; the hub and
// the chat it frames are served by the same hub.
export function frameMessage<T extends ToFrame | FromFrame>(event: Pick<MessageEvent, "origin" | "data">, origin = window.location.origin): T | undefined {
  if (event.origin !== origin) return undefined;
  const data = event.data as {source?: unknown} | null;
  return data && typeof data === "object" && data.source === SOURCE ? (data as T) : undefined;
}

// Framed is the chat the hub's frame shows, once it said it is ready, and
// how many times it did: a chat that loads again (a reload, a restarted
// server) says it again, and the hub tells it the instance again.
export type Framed = {instance: string; ready: number};

// onFrameMessage is what the hub makes of message from the frame that shows
// instance: the frame's state after it, and what the chat asks for.
export function onFrameMessage(
  framed: Framed | undefined,
  message: FromFrame,
  instance: string,
): {framed: Framed | undefined; ask?: "back" | "cancel-update"; open?: string} {
  if (message.type === "ready") {
    return {framed: {instance, ready: framed?.instance === instance ? framed.ready + 1 : 1}};
  }
  if (message.type === "back" || message.type === "cancel-update") return {framed, ask: message.type};
  if (message.type === "open" && typeof message.id === "string" && message.id) return {framed, open: message.id};
  return {framed};
}

// instanceFromHub is the instance a message to the chat in the frame tells
// of: one from the page that frames it, in the protocol.
export function instanceFromHub(event: Pick<MessageEvent, "origin" | "data" | "source">, parent: unknown, origin = window.location.origin): FrameInstance | undefined {
  if (event.source !== parent) return undefined;
  const message = frameMessage<ToFrame>(event, origin);
  return message?.type === "instance" ? message.instance : undefined;
}
