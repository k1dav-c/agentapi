import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import type {DispatchBoard} from "@/lib/hub";
import type {DispatchJob} from "@/lib/hub-dispatch";
import {withDOM} from "@/test/dom";
import {json, restoreFetch, routeFetch} from "@/test/fetch";
import {renderHook, settle} from "@/test/render";
import {useDispatch} from "./use-dispatch";

withDOM();

const job = (over: Partial<DispatchJob> = {}): DispatchJob => ({
  key: "acme/widget#12",
  repo: "acme/widget",
  issue: 12,
  host: "pve",
  agent: "claude",
  instance_id: "pve~acme~widget~12",
  state: "working",
  issue_url: "https://github.com/acme/widget/issues/12",
  credentials: true,
  created_at: "2026-11-09T10:00:00Z",
  updated_at: "2026-11-09T10:00:00Z",
  runs: [],
  round: 1,
  ...over,
});

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
  restoreFetch();
  localStorage.clear();
});

const notConnected = {"/hub/api/github": () => json({connected: false, installations: []})};

describe("useDispatch's jobs", () => {
  test("an action's answer shows until the stream has the job as new", async () => {
    const stopped = job({state: "stopped", updated_at: "2026-11-09T10:05:00Z"});
    routeFetch({...notConnected, "POST /hub/api/dispatch": () => json({job: stopped})});
    const before: DispatchBoard = {hosts: [], jobs: [job()]};
    const hook = await renderHook(({live}: {live?: DispatchBoard}) => useDispatch("/hub", live), {live: before});
    unmount = hook.unmount;
    await settle();

    await act(async () => void (await hook.result.current.actions.stop(job())));
    expect(hook.result.current.jobs.map((j) => j.state)).toEqual(["stopped"]);

    // The stream's event from before the stop, late: the answer stays.
    await hook.rerender({live: {hosts: [], jobs: [job({updated_at: "2026-11-09T10:04:00Z"})]}});
    expect(hook.result.current.jobs.map((j) => j.state)).toEqual(["stopped"]);

    // The stream catches up, and goes on.
    await hook.rerender({live: {hosts: [], jobs: [stopped]}});
    expect(hook.result.current.jobs).toEqual([stopped]);
    const again = job({state: "starting", updated_at: "2026-11-09T10:06:00Z"});
    await hook.rerender({live: {hosts: [], jobs: [again]}});
    expect(hook.result.current.jobs).toEqual([again]);
  });

  test("the machines and the jobs are loading until the stream's first dispatch event", async () => {
    const calls = routeFetch(notConnected);
    const hook = await renderHook(({live}: {live?: DispatchBoard}) => useDispatch("/hub", live), {live: undefined as DispatchBoard | undefined});
    unmount = hook.unmount;
    await settle();
    expect(hook.result.current.hosts).toEqual({state: "loading"});
    expect(hook.result.current.jobs).toEqual([]);
    // Nothing reads them but the stream.
    expect(calls.map((call) => new URL(call.url, "http://hub.test").pathname)).toEqual(["/hub/api/github"]);

    await hook.rerender({live: {hosts: [], jobs: [job()]}});
    expect(hook.result.current.hosts).toEqual({state: "ready", value: []});
    expect(hook.result.current.jobs).toEqual([job()]);
  });
});
