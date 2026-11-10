import {afterEach, beforeAll, describe, expect, test} from "bun:test";
import {withDOM} from "@/test/dom";
import {playChime, setSoundEnabled, soundEnabled} from "./alert-sound";

withDOM();

// FakeAudio counts the notes a chime starts, and whether it was resumed.
const started: number[] = [];
let resumed = 0;
class FakeAudio {
  state = "suspended";
  currentTime = 0;
  destination = {};
  resume() {
    resumed++;
    this.state = "running";
    return Promise.resolve();
  }
  createOscillator() {
    const oscillator = {
      type: "",
      frequency: {value: 0},
      connect: (node: unknown) => node,
      start: () => started.push(oscillator.frequency.value),
      stop: () => {},
    };
    return oscillator;
  }
  createGain() {
    const gain = {gain: {setValueAtTime: () => {}, exponentialRampToValueAtTime: () => {}}, connect: (node: unknown) => node};
    return gain;
  }
}

beforeAll(() => {
  (window as unknown as {AudioContext: unknown}).AudioContext = FakeAudio;
});

afterEach(() => {
  setSoundEnabled(false);
  started.length = 0;
});

describe("the alert sound", () => {
  test("is off until turned on, and the browser keeps the choice", () => {
    expect(soundEnabled()).toBe(false);
    setSoundEnabled(true);
    expect(soundEnabled()).toBe(true);
    expect(window.localStorage.getItem("agentapi.sound")).toBe("on");
  });

  test("chimes only while on: rising for one that needs you, falling for one that finished", () => {
    playChime("needs-you");
    expect(started).toEqual([]);
    setSoundEnabled(true);
    playChime("needs-you");
    playChime("finished");
    expect(started).toEqual([784, 1047, 1047, 784]);
    expect(resumed).toBeGreaterThan(0);
  });

  test("the chime that turning it on plays sounds whatever the choice", () => {
    playChime("needs-you", true);
    expect(started).toEqual([784, 1047]);
  });
});
