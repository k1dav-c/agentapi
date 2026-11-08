"use client";

import type {RefObject} from "react";
import {favoriteInsertion} from "@/lib/hub-favorites";

// useInsertFavorite returns what puts a favorite from the hub (☆) in the
// draft: the draft, or a paragraph after it, with the caret at its end.
// Typed in as an edit (insertText), so the browser's undo takes it back;
// set as the message where that fails, or while the composer is disabled
// (the draft is kept).
export function useInsertFavorite(
  textareaRef: RefObject<HTMLTextAreaElement | null>,
  message: string,
  setMessage: (message: string) => void,
): (text: string) => void {
  return (text: string) => {
    const {from, insert} = favoriteInsertion(message, text);
    const next = message.slice(0, from) + insert;
    const textarea = textareaRef.current;
    if (!textarea || textarea.disabled) {
      setMessage(next);
      return;
    }
    textarea.focus({preventScroll: true});
    textarea.setSelectionRange(from, textarea.value.length);
    let typed = false;
    try {
      typed = document.execCommand("insertText", false, insert);
    } catch {
      // Not supported.
    }
    if (!typed || textarea.value !== next) setMessage(next);
    requestAnimationFrame(() => {
      const end = textarea.value.length;
      textarea.setSelectionRange(end, end);
      textarea.scrollTop = textarea.scrollHeight;
    });
  };
}
