"use client";

import {useEffect} from "react";
import {keyboardInset} from "@/lib/terminal-keys";

// ViewportGuard keeps the page where it can be seen. The pages scroll
// inside their own panes, never the window, in a frame (.app-frame) fixed
// to the visible part of the screen. iOS shrinks neither the page nor a
// fixed frame for the on-screen keyboard: the frame's bottom follows the
// bottom of what the browser reports visible (the visual viewport), the
// same rule in every browser, so the composer and the terminal's key bar
// stay above the keyboard, in a chat the hub frames too. iOS also scrolls
// the window to keep a focused field above the keyboard and often leaves
// it there once the keyboard closes, cutting off the top of the page:
// while nothing is being typed in, the window goes back to the top.
export function ViewportGuard() {
  useEffect(() => {
    const typing = () => document.activeElement?.matches("input, textarea, select, [contenteditable=true], [contenteditable='']") ?? false;
    const reset = () => {
      if (!typing() && (window.scrollX !== 0 || window.scrollY !== 0)) window.scrollTo(0, 0);
    };
    let timer: ReturnType<typeof setTimeout> | undefined;
    // After a field lets go, the keyboard is still closing: wait for it.
    const later = () => {
      clearTimeout(timer);
      timer = setTimeout(reset, 120);
    };
    const viewport = window.visualViewport;
    const root = document.documentElement.style;
    // In a frame (the hub's) the page around it fits itself, and the frame
    // with it.
    const fit = () => {
      if (!viewport || window.top !== window) return;
      const inset = keyboardInset(window.innerHeight, viewport);
      root.setProperty("--keyboard-inset", `${inset}px`);
      root.setProperty("--viewport-top", `${inset ? Math.max(0, Math.round(viewport.offsetTop)) : 0}px`);
      // The home indicator's space is the keyboard's while it is up.
      root.setProperty("--safe-bottom", inset ? "0px" : "env(safe-area-inset-bottom)");
    };
    const onViewport = () => {
      fit();
      later();
    };
    fit();
    window.addEventListener("focusout", later);
    window.addEventListener("scroll", later, {passive: true});
    window.addEventListener("orientationchange", later);
    viewport?.addEventListener("resize", onViewport);
    viewport?.addEventListener("scroll", fit);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("focusout", later);
      window.removeEventListener("scroll", later);
      window.removeEventListener("orientationchange", later);
      viewport?.removeEventListener("resize", onViewport);
      viewport?.removeEventListener("scroll", fit);
    };
  }, []);
  return null;
}
