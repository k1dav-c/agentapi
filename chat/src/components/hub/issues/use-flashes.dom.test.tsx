import {afterEach, describe, expect, jest, test} from "bun:test";
import {act} from "react";
import type {DispatchJob} from "@/lib/hub-dispatch";
import type {Card} from "@/lib/hub-issues";
import {withDOM} from "@/test/dom";
import {renderHook} from "@/test/render";
import {flashFor, useFlashes} from "./use-flashes";

withDOM();

const card = (number: number, state?: DispatchJob["state"]): Card => ({
  key: `acme/widget#${number}`,
  repo: "acme/widget",
  number,
  title: `Issue ${number}`,
  url: `https://github.com/acme/widget/issues/${number}`,
  labels: [],
  body: "",
  job: state ? ({key: `acme/widget#${number}`, state} as DispatchJob) : undefined,
  stage: "todo",
  waiting: false,
});

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
  jest.useRealTimers();
});

const advance = (ms: number) => act(async () => void jest.advanceTimersByTime(ms));

describe("useFlashes", () => {
  test("a flash ends on time while the cards are made again", async () => {
    jest.useFakeTimers();
    const hook = await renderHook(({cards}: {cards: Card[]}) => useFlashes(cards), {cards: [card(1, "starting"), card(2)]});
    unmount = hook.unmount;
    expect(hook.result.current.size).toBe(0);

    await hook.rerender({cards: [card(1, "working"), card(2)]});
    expect([...hook.result.current]).toEqual(["acme/widget#1"]);

    // The hub's stream makes the cards again, the same: the flash goes on,
    // and ends when it was due.
    await advance(flashFor - 400);
    await hook.rerender({cards: [card(1, "working"), card(2)]});
    expect([...hook.result.current]).toEqual(["acme/widget#1"]);
    await advance(400);
    expect(hook.result.current.size).toBe(0);
  });

  test("each card flashes for its own time, and again from the start when it changes again", async () => {
    jest.useFakeTimers();
    const hook = await renderHook(({cards}: {cards: Card[]}) => useFlashes(cards), {cards: [card(1, "starting"), card(2, "working")]});
    unmount = hook.unmount;

    await hook.rerender({cards: [card(1, "working"), card(2, "working")]});
    await advance(1000);
    await hook.rerender({cards: [card(1, "review"), card(2, "review")]});
    expect(new Set(hook.result.current)).toEqual(new Set(["acme/widget#1", "acme/widget#2"]));

    await advance(flashFor - 1);
    expect(new Set(hook.result.current)).toEqual(new Set(["acme/widget#1", "acme/widget#2"]));
    await advance(1);
    expect(hook.result.current.size).toBe(0);
  });
});
