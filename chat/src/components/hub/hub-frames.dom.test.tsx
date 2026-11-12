import {afterAll, beforeAll, describe, expect, mock, spyOn, test} from "bun:test";
import {act} from "react";
import type {HubInstance} from "@/lib/hub";
import {hubInstance} from "@/lib/hub-fixtures";
import {fromFrame, type FromFrame} from "@/lib/hub-frame";
import {withDOM} from "@/test/dom";
import {mount} from "@/test/render";
import {HubFrames} from "./hub-frames";

withDOM();

// The test document loads no page in a frame, and says so for each.
let quiet: ReturnType<typeof spyOn> | undefined;
beforeAll(() => {
  const error = console.error.bind(console);
  quiet = spyOn(console, "error").mockImplementation((...args: unknown[]) => {
    if (!String(args[0]).includes("Iframe page loading is disabled")) error(...args);
  });
});
afterAll(() => quiet?.mockRestore());

const agents = (...ids: string[]) => new Map<string, HubInstance>(ids.map((id) => [id, hubInstance(id, {name: `agent ${id}`})]));

function setup(instances: Map<string, HubInstance>) {
  const handlers = {onBack: mock(), onHome: mock(), onOpen: mock(), onCancelUpdate: mock(), onManageFavorites: mock()};
  const node = (current: string | undefined, all = instances) => (
    <HubFrames current={current ? all.get(current) : undefined} instances={all} hubVersion="4.4.0" root="http://hub.test" favorites={[]} {...handlers} />
  );
  return {handlers, node};
}

// The test document loads no page in a frame: each frame gets a window of
// its own here, which keeps what the hub posts to it.
type FakeWindow = {posted: unknown[]; postMessage: (message: unknown) => void};
function windowOf(frame: HTMLIFrameElement): FakeWindow {
  const known = Object.getOwnPropertyDescriptor(frame, "contentWindow")?.value as FakeWindow | undefined;
  if (known) return known;
  const posted: unknown[] = [];
  const fake = {posted, postMessage: (message: unknown) => void posted.push(message)};
  Object.defineProperty(frame, "contentWindow", {value: fake});
  return fake;
}

const frames = (container: HTMLElement) => [...container.querySelectorAll<HTMLIFrameElement>("iframe")];
const frameOf = (container: HTMLElement, id: string) => container.querySelector<HTMLIFrameElement>(`iframe[data-instance="${id}"]`)!;
// The hub's own bar, which heads an agent until its chat draws one.
const hubBar = (container: HTMLElement) => container.querySelector('[aria-label="Back to Agents"]');

// post is the chat in id's frame posting message to the hub.
const post = (container: HTMLElement, id: string, message: FromFrame) =>
  act(async () => {
    window.dispatchEvent(new MessageEvent("message", {data: message, origin: window.location.origin, source: windowOf(frameOf(container, id)) as unknown as Window}));
  });

describe("HubFrames", () => {
  test("switching back to an agent shows its frame as it was, not loaded again", async () => {
    const {node} = setup(agents("a", "b", "c"));
    const view = await mount(node("a"));
    const a = frameOf(view.container, "a");
    // A frame taken out of the page, even to put it back elsewhere, loads
    // its chat again.
    const removed: Node[] = [];
    const watch = new MutationObserver((records) => records.forEach((record) => removed.push(...record.removedNodes)));
    watch.observe(view.container, {childList: true, subtree: true});
    await view.rerender(node("b"));
    await view.rerender(node("a"));
    await view.rerender(node("b"));
    await view.rerender(node("a"));
    await act(async () => watch.takeRecords().forEach((record) => removed.push(...record.removedNodes)));
    watch.disconnect();
    expect(removed.filter((node) => node instanceof HTMLIFrameElement)).toEqual([]);
    expect(frameOf(view.container, "a")).toBe(a);
    // Neither moved in the page either, which would load it again.
    expect(frames(view.container).map((frame) => frame.dataset.instance)).toEqual(["a", "b"]);
    expect(a.src).toContain("/embed?url=");
    await view.unmount();
  });

  test("the frames out of sight are inert, hidden from screen readers and invisible", async () => {
    const {node} = setup(agents("a", "b"));
    const view = await mount(node("a"));
    await view.rerender(node("b"));
    const [a, b] = [frameOf(view.container, "a"), frameOf(view.container, "b")];
    expect(a.inert).toBe(true);
    expect(a.getAttribute("aria-hidden")).toBe("true");
    expect(a.tabIndex).toBe(-1);
    expect(a.className).toContain("invisible");
    expect(b.inert).toBe(false);
    expect(b.hasAttribute("aria-hidden")).toBe(false);
    expect(b.className).not.toContain("invisible");
    // Without an open agent they stay, all out of sight.
    await view.rerender(node(undefined));
    expect(frames(view.container).map((frame) => frame.inert)).toEqual([true, true]);
    await view.unmount();
  });

  test("a desktop keeps four, and a forgotten agent's frame goes", async () => {
    const all = agents("a", "b", "c", "d", "e");
    const {node} = setup(all);
    const view = await mount(node("a"));
    for (const id of ["b", "c", "d", "e"]) await view.rerender(node(id));
    expect(frames(view.container).map((frame) => frame.dataset.instance)).toEqual(["b", "c", "d", "e"]);
    const without = new Map(all);
    without.delete("c");
    await view.rerender(node("e", without));
    expect(frames(view.container).map((frame) => frame.dataset.instance)).toEqual(["b", "d", "e"]);
    await view.unmount();
  });

  test("a hidden frame's chat doesn't change the bar or act; it is ready for when it shows again", async () => {
    const {node, handlers} = setup(agents("a", "b"));
    const view = await mount(node("a"));
    expect(hubBar(view.container)).not.toBeNull();
    await view.rerender(node("b"));
    // a's chat says it is ready while b shows, b's not yet: the hub's bar
    // still heads b.
    await post(view.container, "a", fromFrame("ready"));
    expect(hubBar(view.container)?.closest("div")?.textContent).toContain("agent b");
    // What a person does in a hidden chat counts for nothing.
    for (const ask of ["back", "home", "cancel-update", "manage-favorites"] as const) await post(view.container, "a", fromFrame(ask));
    await post(view.container, "a", {source: "agentapi.hub", type: "open", id: "b"});
    for (const handler of Object.values(handlers)) expect(handler).not.toHaveBeenCalled();
    // The open one's chat does.
    await post(view.container, "b", fromFrame("back"));
    expect(handlers.onBack).toHaveBeenCalledTimes(1);
    await post(view.container, "b", fromFrame("cancel-update"));
    expect(handlers.onCancelUpdate).toHaveBeenCalledWith("b");
    // Back to a: its chat said it is ready, so its own bar heads it at once.
    await view.rerender(node("a"));
    expect(hubBar(view.container)).toBeNull();
    await view.unmount();
  });

  test("each kept chat is told whether it shows", async () => {
    const {node} = setup(agents("a", "b"));
    const view = await mount(node("a"));
    const a = frameOf(view.container, "a");
    const {posted} = windowOf(a);
    await post(view.container, "a", fromFrame("ready"));
    expect(posted).toContainEqual({source: "agentapi.hub", type: "shown", shown: true});
    expect(posted).toContainEqual(expect.objectContaining({type: "instance"}));
    expect(posted).toContainEqual({source: "agentapi.hub", type: "favorites", favorites: []});
    posted.length = 0;
    await view.rerender(node("b"));
    expect(posted).toEqual([{source: "agentapi.hub", type: "shown", shown: false}]);
    await view.unmount();
  });
});
