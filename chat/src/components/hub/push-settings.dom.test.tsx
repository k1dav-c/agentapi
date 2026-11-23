import {describe, expect, test} from "bun:test";
import {withDOM} from "@/test/dom";
import {button, click, mount} from "@/test/render";
import type {PushDevice, PushPrefsChange} from "@/lib/hub-push";
import {deviceStatus, PushSettingsView} from "./push-settings";

withDOM();

const now = Date.parse("2026-11-23T10:00:00Z");
const device = (id: string, more: Partial<PushDevice> = {}): PushDevice => ({
  id,
  created_at: "2026-11-20T00:00:00Z",
  finished: false,
  issues: false,
  groups: [],
  pinned: false,
  ...more,
});

describe("the Push settings", () => {
  test("list this device first, and change and remove each", async () => {
    const changes: [string, PushPrefsChange][] = [];
    const removed: string[] = [];
    const view = await mount(
      <PushSettingsView
        devices={[device("mac", {label: "Mac · Chrome", groups: ["Client work"], last_error: "the push service answered 429"}), device("phone", {label: "iPhone · Home Screen", finished: true})]}
        error=""
        thisDevice="phone"
        pushHere
        groups={["Client work", "Internal"]}
        now={now}
        onRetry={() => {}}
        onPushHere={() => {}}
        onChange={(id, prefs) => changes.push([id, prefs])}
        onRemove={(id) => removed.push(id)}
      />,
    );
    const cards = [...view.container.querySelectorAll("li")];
    expect(cards.map((card) => card.getAttribute("aria-label"))).toEqual(["iPhone · Home Screen", "Mac · Chrome"]);
    expect(cards[0].textContent).toContain("This device");
    expect(cards[0].textContent).toContain("(all of them)");
    expect(cards[1].textContent).toContain("(only these)");
    expect(cards[1].textContent).toContain("Last push failed: the push service answered 429");

    // The phone: issues on, then follow the pinned agents.
    const checkboxes = cards[0].querySelectorAll<HTMLInputElement>('input[type="checkbox"]');
    expect([...checkboxes].map((box) => box.checked)).toEqual([true, false, false]);
    await click(checkboxes[1]);
    await click(button(cards[0], /Pinned/));
    // The Mac: stop following its group.
    await click(button(cards[1], "Client work"));
    await click(button(cards[1], "Remove Mac · Chrome"));
    expect(changes).toEqual([
      ["phone", {issues: true}],
      ["phone", {pinned: true}],
      ["mac", {groups: []}],
    ]);
    expect(removed).toEqual(["mac"]);
    await view.unmount();
  });

  test("turn quiet hours on in the browser's time zone, and off", async () => {
    const changes: PushPrefsChange[] = [];
    const props = {error: "", thisDevice: "phone", pushHere: true, groups: [], now, onRetry() {}, onPushHere() {}, onRemove() {}, onChange: (_: string, prefs: PushPrefsChange) => changes.push(prefs)};
    const off = await mount(<PushSettingsView devices={[device("phone")]} {...props} />);
    await click(off.container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')[2]);
    expect(changes[0]).toEqual({quiet_hours: {start: "22:00", end: "08:00", time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone}});
    await off.unmount();

    const on = await mount(<PushSettingsView devices={[device("phone", {quiet_hours: {start: "23:00", end: "07:00", time_zone: "Asia/Taipei"}})]} {...props} />);
    expect(on.container.querySelector<HTMLInputElement>('input[aria-label="Quiet from"]')?.value).toBe("23:00");
    expect(on.container.textContent).toContain("Asia/Taipei");
    await click(on.container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')[2]);
    expect(changes[1]).toEqual({quiet_hours: {} as PushPrefsChange["quiet_hours"]});
    await on.unmount();
  });

  test("offer to push here when this browser doesn't get pushes", async () => {
    let asked = 0;
    const view = await mount(
      <PushSettingsView devices={[]} error="" thisDevice={undefined} pushHere={false} groups={[]} now={now} onRetry={() => {}} onPushHere={() => asked++} onChange={() => {}} onRemove={() => {}} />,
    );
    expect(view.container.textContent).toContain("No device gets pushes yet.");
    await click(button(view.container, /Push to this device/));
    expect(asked).toBe(1);
    await view.unmount();
  });
});

test("deviceStatus says the last push and how long the device stays signed in", () => {
  expect(deviceStatus(device("a"), now)).toBe("No push yet");
  expect(deviceStatus(device("a", {last_sent_at: "2026-11-23T09:00:00Z", expires_at: "2026-12-20T09:00:00Z"}), now)).toMatch(/^Last push .+ · signed in until .+/);
});
