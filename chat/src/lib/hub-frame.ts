import {displayName, instanceState, stateLabel, stateRank, type HubInstance, type InstanceState, type InstanceUpdate} from "./hub";
import {instanceVersion} from "./hub-version";
import {parseFavorites, type Favorite} from "./hub-favorites";
import {shownUpdate} from "./hub-update";

// The hub's page and the chat of the instance it opens in a frame talk with
// window messages. The chat draws the bar above itself (one bar, with its
// own actions in it, instead of the hub's bar and a row of the chat's), the
// hub tells it what to show there and the favorites for its composer, and
// the chat asks the hub to go back, to cancel the instance's update, to
// open another agent of its machine, or to open the favorites in the hub's
// settings.

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
  // ran: it has run (hasRun); a hub before 3.6 doesn't say. finished:
  // when an idle one finished (finishedAt), in ms.
  ran?: boolean;
  finished?: number;
  agentType?: string;
  agent?: string;
};

// favorites: all of them, on every ready and every change; a chat before
// 3.5 ignores it, and a hub before it never sends it.
type InstanceToFrame = {source: "agentapi.hub"; type: "instance"; instance: FrameInstance};
type FavoritesToFrame = {source: "agentapi.hub"; type: "favorites"; favorites: Favorite[]};
export type ToFrame = InstanceToFrame | FavoritesToFrame;
export type FrameAsk = "ready" | "back" | "cancel-update" | "manage-favorites";
export type FromFrame = {source: "agentapi.hub"; type: FrameAsk} | {source: "agentapi.hub"; type: "open"; id: string};

const SOURCE = "agentapi.hub";

export function toFrame(instance: FrameInstance): InstanceToFrame {
  return {source: SOURCE, type: "instance", instance};
}

// favoritesToFrame tells the chat the favorites for its composer.
export function favoritesToFrame(favorites: Favorite[]): FavoritesToFrame {
  return {source: SOURCE, type: "favorites", favorites};
}

export function fromFrame(type: FrameAsk): FromFrame {
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

// The parsers below read a message of the protocol from what the other end
// posted: undefined for anything else (another source, a message of
// another type, a field missing or of the wrong type), so neither end
// takes the other's word for its shape. A field a newer end adds is left
// out; a state this end doesn't know is unknown.

type Fields = Record<string, unknown>;

function fields(value: unknown): Fields | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? (value as Fields) : undefined;
}

function text(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function stateOf(value: unknown): InstanceState {
  return typeof value === "string" && value !== "unknown" && Object.hasOwn(stateRank, value) ? (value as InstanceState) : "unknown";
}

// parseUpdate reads an update's chip: its state, and what the chip says.
function parseUpdate(value: unknown): InstanceUpdate | undefined {
  const update = fields(value);
  if (!update || typeof update.state !== "string" || typeof update.allowed !== "boolean") return undefined;
  return {
    allowed: update.allowed,
    state: update.state,
    keeps_conversation: update.keeps_conversation === true,
    keeps_session: update.keeps_session === true,
    ...(text(update.target) !== undefined ? {target: text(update.target)} : {}),
    ...(text(update.error) !== undefined ? {error: text(update.error)} : {}),
    ...(text(update.blocked) !== undefined ? {blocked: text(update.blocked)} : {}),
  };
}

function parseMachineAgent(value: unknown): MachineAgent | undefined {
  const agent = fields(value);
  const id = text(agent?.id);
  if (!agent || !id || text(agent.name) === undefined || text(agent.label) === undefined) return undefined;
  return {
    id,
    name: text(agent.name)!,
    state: stateOf(agent.state),
    label: text(agent.label)!,
    ...(typeof agent.ran === "boolean" ? {ran: agent.ran} : {}),
    ...(typeof agent.finished === "number" && Number.isFinite(agent.finished) ? {finished: agent.finished} : {}),
    ...(text(agent.agentType) ? {agentType: text(agent.agentType)} : {}),
    ...(text(agent.agent) ? {agent: text(agent.agent)} : {}),
  };
}

// parseFrameInstance reads what the hub tells of the instance: its ID,
// name, state, label and tab at least.
function parseFrameInstance(value: unknown): FrameInstance | undefined {
  const instance = fields(value);
  const id = text(instance?.id);
  if (!instance || !id || text(instance.name) === undefined || text(instance.label) === undefined || text(instance.tab) === undefined) return undefined;
  const optional = (key: keyof FrameInstance) => (text(instance[key]) ? {[key]: text(instance[key])} : {});
  const machine = Array.isArray(instance.machine) ? instance.machine.map(parseMachineAgent).filter((agent) => agent !== undefined) : [];
  const update = parseUpdate(instance.update);
  return {
    id,
    name: text(instance.name)!,
    state: stateOf(instance.state),
    label: text(instance.label)!,
    ...optional("agentType"),
    ...optional("agent"),
    ...optional("hostname"),
    ...optional("version"),
    ...optional("hubVersion"),
    ...(typeof instance.lastKnown === "boolean" ? {lastKnown: instance.lastKnown} : {}),
    ...(update ? {update} : {}),
    tab: text(instance.tab)!,
    ...(machine.length > 1 ? {machine} : {}),
  };
}

// parseToFrame reads a message the hub posted to the chat in its frame.
export function parseToFrame(data: unknown): ToFrame | undefined {
  const message = fields(data);
  if (message?.source !== SOURCE) return undefined;
  switch (message.type) {
    case "instance": {
      const instance = parseFrameInstance(message.instance);
      return instance && toFrame(instance);
    }
    case "favorites": {
      const favorites = parseFavorites(message.favorites);
      return favorites && favoritesToFrame(favorites);
    }
    default:
      return undefined;
  }
}

const asks: readonly FrameAsk[] = ["ready", "back", "cancel-update", "manage-favorites"];

// parseFromFrame reads a message the chat in the frame posted to the hub.
export function parseFromFrame(data: unknown): FromFrame | undefined {
  const message = fields(data);
  if (message?.source !== SOURCE) return undefined;
  if (message.type === "open") {
    const id = text(message.id);
    return id ? openFromFrame(id) : undefined;
  }
  const ask = asks.find((type) => type === message.type);
  return ask && fromFrame(ask);
}

// messageFromHub is what the page that frames the chat (parent) posted to
// it, of the same origin: the hub and the chat it frames are served by the
// same hub.
export function messageFromHub(event: Pick<MessageEvent, "origin" | "data" | "source">, parent: unknown, origin = window.location.origin): ToFrame | undefined {
  return event.source === parent && event.origin === origin ? parseToFrame(event.data) : undefined;
}

// messageFromFrame is what the chat in the hub's frame (its window, frame)
// posted to the hub, of the same origin.
export function messageFromFrame(event: Pick<MessageEvent, "origin" | "data" | "source">, frame: unknown, origin = window.location.origin): FromFrame | undefined {
  return !!frame && event.source === frame && event.origin === origin ? parseFromFrame(event.data) : undefined;
}

// Framed is the chat the hub's frame shows, once it said it is ready, and
// how many times it did: a chat that loads again (a reload, a restarted
// server) says it again, and the hub tells it the instance again.
export type Framed = {instance: string; ready: number};

// readyAgain is the frame's state after the chat that shows instance says
// it is ready: another ready of the same chat, or the first of another.
export function readyAgain(framed: Framed | undefined, instance: string): Framed {
  return {instance, ready: framed?.instance === instance ? framed.ready + 1 : 1};
}
