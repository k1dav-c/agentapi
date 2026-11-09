"use client";

import {useCallback, useMemo} from "react";
import {toast} from "sonner";
import {displayName, type HubInstance} from "@/lib/hub";
import {createHubAPI, hubErrorText} from "@/lib/hub-api";
import {forgotText} from "@/lib/hub-filter";
import {useLatest} from "@/lib/use-stable-callback";

// useHubActions makes the changes the hub page asks for (lib/hub-api.ts).
// Each comes back on the event stream, like any other; one the hub
// refuses, or doesn't get, says so in a toast, and resolves to false. The
// functions keep their identity: they read the latest instances.
export function useHubActions(root: string, instances: ReadonlyMap<string, HubInstance>) {
  const api = useMemo(() => createHubAPI(root), [root]);
  const instancesRef = useLatest(instances);
  const attempt = useCallback(async (failure: (name: string) => string, id: string, change: () => Promise<void>): Promise<boolean> => {
    try {
      await change();
      return true;
    } catch (error) {
      const instance = instancesRef.current.get(id);
      toast.error(failure(instance ? displayName(instance) : id), {description: hubErrorText(error)});
      return false;
    }
  }, [instancesRef]);
  return useMemo(
    () => ({
      // The instance by its ID, as the hub said last.
      instance: (id: string) => instancesRef.current.get(id),
      // Pins an instance below the pinned ones on the hub, or unpins it.
      setPinned: (id: string, pinned: boolean) =>
        attempt((name) => `Couldn't ${pinned ? "pin" : "unpin"} ${name}`, id, () => api.changeInstance(id, {pinned})),
      setGroup: (id: string, group: string) =>
        attempt((name) => (group ? `Couldn't move ${name} to ${group}` : `Couldn't take ${name} out of its group`), id, () => api.changeInstance(id, {group})),
      // Mutes what an instance waits for now, until that changes (the hub
      // ends the mute then), or ends the mute.
      setMuted: (id: string, muted: boolean) => attempt((name) => `Couldn't ${muted ? "mute" : "unmute"} ${name}`, id, () => api.changeInstance(id, {muted})),
      forget: (id: string) => attempt((name) => `Couldn't forget ${name}`, id, () => api.forgetInstance(id)),
      // Forgets the stopped ones of ids at once, with one toast, and
      // returns those the hub forgot.
      forgetStopped: async (ids: string[]): Promise<string[]> => {
        let forgot: string[];
        try {
          forgot = await api.forgetStopped(ids);
        } catch (error) {
          toast.error(`Couldn't forget ${ids.length} stopped ${ids.length === 1 ? "agent" : "agents"}`, {description: hubErrorText(error)});
          return [];
        }
        const {ok, title, description} = forgotText(ids.length, forgot.length);
        if (ok && !description) toast.success(title);
        else if (ok) toast.warning(title, {description});
        else toast.error(title, {description});
        return forgot;
      },
      // The order of the pinned ones, after id moved.
      setPins: (ids: string[], id: string) => attempt((name) => `Couldn't move ${name}`, id, () => api.setPins(ids)),
      // Moves the members of a group to another, with one toast for those
      // the hub refused.
      moveGroup: async (members: HubInstance[], to: string) => {
        const results = await Promise.allSettled(members.map((instance) => api.changeInstance(instance.id, {group: to})));
        const failed = results.flatMap((result) => (result.status === "rejected" ? [result.reason as unknown] : []));
        if (failed.length > 0) {
          toast.error(`Couldn't move ${failed.length} of ${members.length} ${members.length === 1 ? "agent" : "agents"} to ${to}`, {
            description: hubErrorText(failed[0]),
          });
        }
      },
    }),
    [api, attempt, instancesRef],
  );
}
