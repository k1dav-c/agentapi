"use client";

import {useEffect, useRef, useState} from "react";
import type {Card} from "@/lib/hub-issues";
import {lookOf, type Look} from "./job-glyph";

// How long a card flashes.
export const flashFor = 1600;

// useFlashes are the cards whose look changed since the last read, for a
// moment: the ring they flash in their new state's color says what moved.
// Each card has a timer of its own: the cards are made again on every
// event of the hub's stream, which doesn't cut a flash short or keep it
// on; a card that changes again flashes again from the start.
export function useFlashes(cards: Card[]): ReadonlySet<string> {
  const looks = useRef<Map<string, Look> | null>(null);
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const [flashing, setFlashing] = useState<ReadonlySet<string>>(new Set());
  useEffect(() => {
    const next = new Map(cards.map((card) => [card.key, lookOf(card)]));
    const before = looks.current;
    looks.current = next;
    if (!before) return;
    const changed = [...next].filter(([key, look]) => before.has(key) && before.get(key) !== look).map(([key]) => key);
    if (changed.length === 0) return;
    setFlashing((keys) => new Set([...keys, ...changed]));
    for (const key of changed) {
      clearTimeout(timers.current.get(key));
      const timer = setTimeout(() => {
        timers.current.delete(key);
        setFlashing((keys) => new Set([...keys].filter((other) => other !== key)));
      }, flashFor);
      timers.current.set(key, timer);
    }
  }, [cards]);
  useEffect(() => {
    const pending = timers.current;
    return () => {
      for (const timer of pending.values()) clearTimeout(timer);
      pending.clear();
    };
  }, []);
  return flashing;
}
