"use client";

import {useLayoutEffect, useRef, type RefObject} from "react";

const reduced = () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// useFlip moves the cards of container (each [data-flip] with its key) to
// their new places instead of jumping there: a card whose stage changed
// glides from its old section to its new one, and the others close up
// behind it. It measures before the browser paints and plays the
// difference back (First, Last, Invert, Play), with the Web Animations
// API so nothing re-renders. A card that wasn't there rises in; none of it
// moves under reduced motion.
export function useFlip(container: RefObject<HTMLElement | null>, signature: string) {
  const last = useRef(new Map<string, DOMRect>());
  useLayoutEffect(() => {
    const root = container.current;
    if (!root) return;
    const cards = [...root.querySelectorAll<HTMLElement>("[data-flip]")];
    const next = new Map<string, DOMRect>();
    for (const card of cards) next.set(card.dataset.flip!, card.getBoundingClientRect());
    const before = last.current;
    last.current = next;
    // The first layout has nothing to move from.
    if (before.size === 0 || reduced()) return;
    for (const card of cards) {
      const from = before.get(card.dataset.flip!);
      const to = next.get(card.dataset.flip!)!;
      if (!from) {
        card.animate([{opacity: 0, transform: "translateY(8px) scale(0.98)"}, {opacity: 1, transform: "none"}], {duration: 320, easing: "cubic-bezier(0.2, 0.8, 0.2, 1)"});
        continue;
      }
      const dx = from.left - to.left;
      const dy = from.top - to.top;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
      // Far moves take a little longer, so the eye can follow them.
      const duration = Math.min(560, 260 + Math.hypot(dx, dy) * 0.35);
      // A card that changed sections passes over the others, lifted.
      const far = Math.abs(dy) > to.height * 1.5;
      const move = card.animate(
        far
          ? [
              {transform: `translate(${dx}px, ${dy}px)`, boxShadow: "0 1px 2px rgb(0 0 0 / 0.05)"},
              {transform: `translate(${dx * 0.5}px, ${dy * 0.5}px) scale(1.015)`, boxShadow: "0 18px 40px -12px rgb(0 0 0 / 0.28)", offset: 0.5},
              {transform: "none", boxShadow: "0 1px 2px rgb(0 0 0 / 0.05)"},
            ]
          : [{transform: `translate(${dx}px, ${dy}px)`}, {transform: "none"}],
        {duration, easing: "cubic-bezier(0.2, 0.8, 0.2, 1)"},
      );
      if (far) {
        card.style.zIndex = "5";
        move.onfinish = move.oncancel = () => {
          card.style.zIndex = "";
        };
      }
    }
  }, [container, signature]);
}
