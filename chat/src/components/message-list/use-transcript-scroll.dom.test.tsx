import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {renderHook} from "@/test/render";
import {noteSent, useTranscriptScroll} from "./use-transcript-scroll";

withDOM();

// A scroll area with a height the test sets (happy-dom lays nothing out),
// that records where the transcript scrolled it.
function area(clientHeight: number) {
  const el = document.createElement("div");
  el.append(document.createElement("div"));
  let height = clientHeight;
  const scrolls: number[] = [];
  Object.defineProperty(el, "clientHeight", {get: () => clientHeight});
  Object.defineProperty(el, "scrollHeight", {get: () => height});
  el.scrollTo = ((options: ScrollToOptions) => {
    scrolls.push(options.top ?? 0);
    el.scrollTop = options.top ?? 0;
  }) as typeof el.scrollTo;
  document.body.append(el);
  return {
    el,
    scrolls,
    grow: (by: number) => (height += by),
    // The reader scrolls to top, as a scroll event tells the transcript.
    readAt: (top: number) => {
      el.scrollTop = top;
      el.dispatchEvent(new Event("scroll"));
    },
  };
}

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
});

describe("the transcript's scroll", () => {
  test("a reader further up stays there while the agent answers their message; their own next message takes them to the end", async () => {
    const a = area(500);
    a.grow(2000);
    const props = (content: unknown) => ({
      // The last message is the reader's: the agent's reply streams into the
      // timeline, not the messages.
      messages: [{role: "user"}],
      content,
      serverStatus: "running" as const,
      layout: "",
    });
    const hook = await renderHook((p: ReturnType<typeof props>) => useTranscriptScroll(a.el, p), props({}));
    unmount = hook.unmount;
    expect(a.scrolls.at(-1)).toBe(a.el.scrollHeight);

    // The reader scrolls up to read; the reply keeps streaming.
    await act(async () => a.readAt(900));
    a.scrolls.length = 0;
    for (let i = 0; i < 3; i++) {
      a.grow(150);
      await hook.rerender(props({i}));
    }
    expect(a.scrolls).toEqual([]);
    expect(a.el.scrollTop).toBe(900);
    expect(hook.result.current.unreadCount).toBe(3);
    expect(hook.result.current.showScrollButton).toBe(true);

    // They send a message: to the end.
    await act(async () => noteSent());
    expect(a.scrolls.at(-1)).toBe(a.el.scrollHeight);
    expect(hook.result.current.unreadCount).toBe(0);
  });

  test("a reader at the end follows what arrives", async () => {
    const a = area(500);
    a.grow(1000);
    const props = (content: unknown) => ({messages: [{role: "user"}], content, serverStatus: "running" as const, layout: ""});
    const hook = await renderHook((p: ReturnType<typeof props>) => useTranscriptScroll(a.el, p), props({}));
    unmount = hook.unmount;
    a.grow(200);
    await hook.rerender(props({next: true}));
    expect(a.scrolls.at(-1)).toBe(a.el.scrollHeight);
  });
});
