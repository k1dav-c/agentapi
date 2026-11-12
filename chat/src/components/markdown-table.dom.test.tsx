import {afterEach, describe, expect, mock, test} from "bun:test";
import React, {act} from "react";
import {withDOM} from "@/test/dom";
import {button, click, mount} from "@/test/render";
import {ProcessedMessage} from "./processed-message";

withDOM();

// Radix picks its layout effect as it loads, a no-op without a document
// (test/dom.ts), and its dialog's portal waits for that effect to show.
// Here it is React's, so the dialog opens.
mock.module("@radix-ui/react-use-layout-effect", () => ({useLayoutEffect: React.useLayoutEffect}));

let view: Awaited<ReturnType<typeof mount>> | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
});

const twoTables = [
  "| Name | Status |",
  "| --- | --- |",
  "| Build | Passed |",
  "",
  "Between.",
  "",
  "| Step | Took |",
  "| --- | --- |",
  "| Lint | 3s |",
].join("\n");

async function show(content: string) {
  view = await mount(<ProcessedMessage messageContent={content} isUser={false} renderMode="markdown" />);
  return view.container;
}

const dialog = () => document.querySelector<HTMLElement>("[role=dialog]");
const expandButtons = (within: ParentNode) =>
  [...within.querySelectorAll("button")].filter((b) => b.getAttribute("aria-label") === "Open the table");

describe("MarkdownTable", () => {
  test("each table has a button that opens it in a dialog", async () => {
    const container = await show(twoTables);
    expect(container.querySelectorAll("table")).toHaveLength(2);
    const buttons = expandButtons(container);
    expect(buttons).toHaveLength(2);
    expect(buttons[0].title).toBe("Open the table");
    expect(dialog()).toBeNull();

    await click(buttons[1]);
    const opened = dialog()!;
    expect(opened).not.toBeNull();
    expect(opened.textContent).toContain("Table");
    const cells = [...opened.querySelectorAll("th, td")].map((cell) => cell.textContent);
    expect(cells).toEqual(["Step", "Took", "Lint", "3s"]);
    // The popup's table has no button to open it again.
    expect(expandButtons(opened)).toHaveLength(0);

    await click(button(opened, "Close"));
    expect(dialog()).toBeNull();
  });

  test("a wide table shades its right edge until it is scrolled to the end", async () => {
    const container = await show(twoTables);
    const scroller = container.querySelector("table")!.parentElement!;
    const shade = () => container.querySelector("[data-slot=table-more]");
    expect(shade()).toBeNull();

    Object.defineProperties(scroller, {
      scrollWidth: {configurable: true, value: 800},
      clientWidth: {configurable: true, value: 300},
      scrollLeft: {configurable: true, writable: true, value: 0},
    });
    await act(async () => scroller.dispatchEvent(new Event("scroll")));
    expect(shade()).not.toBeNull();

    scroller.scrollLeft = 500;
    await act(async () => scroller.dispatchEvent(new Event("scroll")));
    expect(shade()).toBeNull();
  });
});
