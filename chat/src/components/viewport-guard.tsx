"use client";

import {useEffect} from "react";

// ViewportGuard keeps the window where it belongs. The pages scroll inside
// their own panes, never the window; but iOS scrolls the window to keep a
// focused field above the keyboard and often leaves it there once the
// keyboard closes, cutting off the top of the page. While nothing is being
// typed in, the window goes back to the top.
export function ViewportGuard() {
  useEffect(() => {
    const typing = () => document.activeElement?.matches("input, textarea, select, [contenteditable=true], [contenteditable='']") ?? false;
    const reset = () => {
      if (!typing() && (window.scrollY !== 0 || window.scrollX !== 0)) window.scrollTo(0, 0);
    };
    let timer: ReturnType<typeof setTimeout> | undefined;
    // After a field lets go, the keyboard is still closing: wait for it.
    const later = () => {
      clearTimeout(timer);
      timer = setTimeout(reset, 120);
    };
    const viewport = window.visualViewport;
    window.addEventListener("focusout", later);
    window.addEventListener("scroll", later, {passive: true});
    window.addEventListener("orientationchange", later);
    viewport?.addEventListener("resize", later);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("focusout", later);
      window.removeEventListener("scroll", later);
      window.removeEventListener("orientationchange", later);
      viewport?.removeEventListener("resize", later);
    };
  }, []);
  return null;
}
