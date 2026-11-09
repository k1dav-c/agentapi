// Renders into the test document (withDOM, ./dom), with
// effects: mount, render again with other props, and unmount, each inside
// act() so what they set off has happened when it returns.

import {act, type ReactNode} from "react";
import type {Root} from "react-dom/client";

export interface Mounted {
  container: HTMLElement;
  rerender: (node: ReactNode) => Promise<void>;
  unmount: () => Promise<void>;
}

export async function mount(node: ReactNode): Promise<Mounted> {
  const container = document.createElement("div");
  document.body.append(container);
  // Loaded here, not imported: react-dom/client decides as it loads
  // whether there is a DOM (and its input events with it), and the test
  // file's imports load before its document is there.
  const {createRoot} = await import("react-dom/client");
  let root: Root | undefined;
  await act(async () => {
    root = createRoot(container);
    root.render(node);
  });
  return {
    container,
    rerender: (next) => act(async () => root!.render(next)),
    unmount: async () => {
      await act(async () => root!.unmount());
      container.remove();
    },
  };
}

// settle lets the promises under way (a mocked fetch's) resolve, and what
// they set render.
export const settle = () => act(async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve();
});

// click presses an element as a person would.
export const click = (element: Element | null | undefined) =>
  act(async () => {
    if (!element) throw new Error("nothing to click");
    (element as HTMLElement).click();
  });

// key sends a key down to the element (or the one with the focus).
export const key = (key: string, element: Element | null = document.activeElement) =>
  act(async () => {
    (element ?? document.body).dispatchEvent(new KeyboardEvent("keydown", {key, bubbles: true, cancelable: true}));
  });

// button finds a button by what it says or its label.
export function button(within: ParentNode, name: string | RegExp): HTMLButtonElement | undefined {
  const matches = (text: string | null) => !!text && (typeof name === "string" ? text.trim() === name : name.test(text.trim()));
  return [...within.querySelectorAll("button")].find((b) => matches(b.getAttribute("aria-label")) || matches(b.textContent));
}

// renderHook calls hook in a component and keeps what it returned last:
// result.current. rerender calls it again with other arguments.
export async function renderHook<A, R>(hook: (args: A) => R, args: A) {
  const result = {current: undefined as R};
  function Probe({args}: {args: A}) {
    result.current = hook(args);
    return null;
  }
  const mounted = await mount(<Probe args={args} />);
  return {result, rerender: (next: A) => mounted.rerender(<Probe args={next} />), unmount: mounted.unmount};
}
