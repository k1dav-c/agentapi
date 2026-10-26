"use client";

import {useEffect} from "react";
import {viewportFrame} from "@/lib/viewport-frame";

// ViewportGuard keeps the page where it can be seen. The pages scroll
// inside their own panes, never the window, in a frame (.app-frame) fixed
// to the visible part of the screen. iOS shrinks no fixed frame for the
// on-screen keyboard: while it is up the frame takes the place and size of
// what the browser reports visible (the visual viewport, viewportFrame),
// the same rule in every browser, so the composer and the terminal's key
// bar stay above the keyboard, in a chat the hub frames too. iOS also
// scrolls the window to keep a focused field above the keyboard and often
// leaves it there once the keyboard closes, cutting off the top of the
// page: while nothing is being typed in, the window goes back to the top.
export function ViewportGuard() {
  useEffect(() => {
    // What has the focus, also inside a frame (the chat the hub opens, where
    // the window's own focus is only the frame): while a field there is
    // typed in, the window stays where iOS scrolled it for the keyboard.
    const focused = () => {
      let element = document.activeElement;
      while (element instanceof HTMLIFrameElement) {
        try {
          element = element.contentDocument?.activeElement ?? null;
        } catch {
          return element;
        }
      }
      return element;
    };
    const typing = () => focused()?.matches("input, textarea, select, [contenteditable=true], [contenteditable='']") ?? false;
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
    const html = document.documentElement;
    // In a frame (the hub's) the page around it fits itself, and the frame
    // with it.
    const fit = () => {
      if (!viewport || window.top !== window) return;
      const frame = viewportFrame(window.innerHeight, viewport);
      if (frame) {
        html.style.setProperty("--viewport-top", `${frame.top}px`);
        html.style.setProperty("--viewport-height", `${frame.height}px`);
        // The home indicator's space is the keyboard's while it is up.
        html.style.setProperty("--safe-bottom", "0px");
        html.dataset.viewportFrame = "";
      } else {
        html.style.removeProperty("--safe-bottom");
        delete html.dataset.viewportFrame;
      }
    };
    const onViewport = () => {
      fit();
      later();
    };
    // The browser keeps adjusting after the keyboard is up (a toolbar that
    // hides, the window scrolled to the field), not always with an event
    // of the visual viewport: fit again a few times over the next second.
    let settling: ReturnType<typeof setTimeout>[] = [];
    const settle = () => {
      settling.forEach(clearTimeout);
      settling = [50, 150, 300, 600, 1000].map((delay) => setTimeout(fit, delay));
    };
    const onFocusOut = () => {
      later();
      settle();
    };
    fit();
    window.addEventListener("focusin", settle);
    // A field in a frame (the chat the hub opens) takes the focus from the
    // window instead.
    window.addEventListener("blur", settle);
    window.addEventListener("focusout", onFocusOut);
    window.addEventListener("resize", onViewport);
    window.addEventListener("scroll", onViewport, {passive: true});
    window.addEventListener("orientationchange", later);
    viewport?.addEventListener("resize", onViewport);
    viewport?.addEventListener("scroll", fit);
    return () => {
      clearTimeout(timer);
      settling.forEach(clearTimeout);
      window.removeEventListener("focusin", settle);
      window.removeEventListener("blur", settle);
      window.removeEventListener("focusout", onFocusOut);
      window.removeEventListener("resize", onViewport);
      window.removeEventListener("scroll", onViewport);
      window.removeEventListener("orientationchange", later);
      viewport?.removeEventListener("resize", onViewport);
      viewport?.removeEventListener("scroll", fit);
    };
  }, []);
  return null;
}
