import {describe, expect, test} from "bun:test";
import {alertForTransition, faviconDataUrl} from "./background-alerts";

describe("alertForTransition", () => {
  test("starting to wait for the user", () => {
    expect(alertForTransition("working", "needs-you")).toBe("needs-you");
    expect(alertForTransition("ready", "needs-you")).toBe("needs-you");
  });
  test("still waiting is not a new alert", () => {
    expect(alertForTransition("needs-you", "needs-you")).toBeNull();
  });
  test("finishing work", () => {
    expect(alertForTransition("working", "ready")).toBe("finished");
  });
  test("answering a prompt or reconnecting is not 'finished'", () => {
    expect(alertForTransition("needs-you", "ready")).toBeNull();
    expect(alertForTransition("reconnecting", "ready")).toBeNull();
    expect(alertForTransition("connecting", "ready")).toBeNull();
  });
});

describe("faviconDataUrl", () => {
  test("adds a state dot only when there is a badge", () => {
    expect(decodeURIComponent(faviconDataUrl(null, "#fff"))).not.toContain("<circle");
    expect(decodeURIComponent(faviconDataUrl("needs-you", "#fff"))).toContain('fill="#c27c0e"');
  });
});
