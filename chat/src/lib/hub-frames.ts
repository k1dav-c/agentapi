// The hub keeps the chats of the agents opened last loaded in their frames,
// hidden behind the open one, so going back to one shows it at once instead
// of loading the chat again (its cache, then its events through the hub).
// Each frame is a whole chat with its own stream of events: a desktop keeps
// a few, a phone fewer.
export const KEPT_FRAMES = 4;
export const KEPT_FRAMES_PHONE = 2;

// keptFrames is the agents whose frames stay, most recently opened first:
// the open one (current) first, then those of kept that the hub still has
// (exists), at most max. Without an open agent the list stays as it was,
// less the agents the hub forgot. The same array when nothing changed, so
// a caller can compare.
export function keptFrames(kept: readonly string[], current: string | undefined, exists: (id: string) => boolean, max: number): readonly string[] {
  const next = [...(current !== undefined && exists(current) ? [current] : []), ...kept.filter((id) => id !== current && exists(id))].slice(0, Math.max(1, max));
  return next.length === kept.length && next.every((id, i) => id === kept[i]) ? kept : next;
}

// frameReady counts another ready of the chat of id: a frame's chat says it
// is ready on every load (a reload, a restarted server), and the hub tells
// it the instance again. The same map when id isn't kept, whose frame is
// gone.
export function frameReady(readies: ReadonlyMap<string, number>, id: string, kept: readonly string[]): ReadonlyMap<string, number> {
  if (!kept.includes(id)) return readies;
  return new Map(readies).set(id, (readies.get(id) ?? 0) + 1);
}

// keptReadies drops the readies of the frames no longer kept: one opened
// again loads again, and shows the hub's bar until its chat is ready. The
// same map when none went.
export function keptReadies(readies: ReadonlyMap<string, number>, kept: readonly string[]): ReadonlyMap<string, number> {
  const next = new Map([...readies].filter(([id]) => kept.includes(id)));
  return next.size === readies.size ? readies : next;
}

// frameOrder is the order the kept frames stand in the page: the one there
// before keep their places, and a new one goes last. A frame moved in the
// page loads its chat again, so the frames never move, whichever shows.
// The same array when nothing changed.
export function frameOrder(order: readonly string[], kept: readonly string[]): readonly string[] {
  const next = [...order.filter((id) => kept.includes(id)), ...kept.filter((id) => !order.includes(id))];
  return next.length === order.length && next.every((id, i) => id === order[i]) ? order : next;
}
