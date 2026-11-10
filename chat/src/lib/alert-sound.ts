// Alert sounds: a short chime when an agent needs you or finishes, made with
// Web Audio so that no sound file ships with the chat, and so that it plays
// on a plain-http page, where browsers have no notifications. The choice is
// this browser's, shared by the hub and the chats it opens.

import {useEffect, useSyncExternalStore} from "react";

export type ChimeKind = "needs-you" | "finished";

const PREFERENCE_KEY = "agentapi.sound";
const PREFERENCE_EVENT = "agentapi-sound-change";

export function soundEnabled(): boolean {
  try {
    return window.localStorage.getItem(PREFERENCE_KEY) === "on";
  } catch {
    return false;
  }
}

export function setSoundEnabled(enabled: boolean) {
  try {
    if (enabled) window.localStorage.setItem(PREFERENCE_KEY, "on");
    else window.localStorage.removeItem(PREFERENCE_KEY);
  } catch {
    // Without storage the choice lasts for this page only.
  }
  window.dispatchEvent(new Event(PREFERENCE_EVENT));
}

function subscribe(listener: () => void): () => void {
  window.addEventListener(PREFERENCE_EVENT, listener);
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(PREFERENCE_EVENT, listener);
    window.removeEventListener("storage", listener);
  };
}

// useSound is whether this browser chimes, and toggleSound, which turns it
// on with a chime to hear it by, or off.
export function useSound(): {sound: boolean; toggleSound: () => void} {
  const sound = useSyncExternalStore(subscribe, soundEnabled, () => false);
  const toggleSound = () => {
    setSoundEnabled(!sound);
    if (!sound) playChime("needs-you", true);
  };
  return {sound, toggleSound};
}

let context: AudioContext | undefined;

function audio(): AudioContext | undefined {
  if (context) return context;
  const Context = typeof window === "undefined" ? undefined : window.AudioContext;
  if (!Context) return undefined;
  context = new Context();
  return context;
}

// useSoundUnlock lets the page's next click or key start the audio, which
// browsers keep suspended until a person did something on the page: a
// chime then plays even after the tab went to the background.
export function useSoundUnlock() {
  useEffect(() => {
    const unlock = () => {
      if (!soundEnabled()) return;
      const audioContext = audio();
      if (audioContext?.state === "suspended") void audioContext.resume();
    };
    window.addEventListener("pointerdown", unlock, true);
    window.addEventListener("keydown", unlock, true);
    return () => {
      window.removeEventListener("pointerdown", unlock, true);
      window.removeEventListener("keydown", unlock, true);
    };
  }, []);
}

// awayFromPage is whether a person is likely not looking at the page: its
// tab is in the background, or another window has the focus.
export function awayFromPage(): boolean {
  return document.hidden || !document.hasFocus();
}

// The notes of each chime, in Hz: rising for one that needs you, falling
// for one that finished.
const notes: Record<ChimeKind, number[]> = {
  "needs-you": [784, 1047],
  finished: [1047, 784],
};

// playChime plays kind's chime if the sound is on (or always, for the one
// that turning it on plays).
export function playChime(kind: ChimeKind, always = false) {
  if (!always && !soundEnabled()) return;
  const audioContext = audio();
  if (!audioContext) return;
  if (audioContext.state === "suspended") void audioContext.resume();
  const start = audioContext.currentTime + 0.02;
  notes[kind].forEach((frequency, i) => {
    const at = start + i * 0.16;
    const oscillator = audioContext.createOscillator();
    const gain = audioContext.createGain();
    oscillator.type = "sine";
    oscillator.frequency.value = frequency;
    gain.gain.setValueAtTime(0.0001, at);
    gain.gain.exponentialRampToValueAtTime(0.18, at + 0.015);
    gain.gain.exponentialRampToValueAtTime(0.0001, at + 0.4);
    oscillator.connect(gain).connect(audioContext.destination);
    oscillator.start(at);
    oscillator.stop(at + 0.42);
  });
}
