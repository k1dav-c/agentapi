"use client";

import {useCallback, useEffect, useLayoutEffect, useRef, useState} from "react";
import type {ServerStatus} from "@/lib/agent-status";
import {getNextUserMessageTop, getPreviousUserMessageTop} from "@/lib/scroll-anchors";

// Below this height the transcript hides its floating buttons (two rows
// of them would cover what little of it shows).
const FLOATING_BUTTONS_MIN_HEIGHT = 140;

// useTranscriptScroll keeps the transcript in scrollArea pinned to its
// end while the reader is there, counts the updates that arrive while they
// read further up, and moves between the tasks' prompts. content is a new
// object whenever what the transcript shows changes (the timeline: its
// objects stay while nothing changes); layout changes with what changes its
// size.
export function useTranscriptScroll(
  scrollArea: HTMLDivElement | null,
  {
    messages,
    content,
    serverStatus,
    layout,
  }: {
    messages: readonly {role: string}[];
    content: unknown;
    serverStatus: ServerStatus;
    layout: string;
  },
) {
  const userMessageCount = messages.filter((message) => message.role === "user").length;
  const [showScrollButton, setShowScrollButton] = useState(false);
  // Whether the transcript is tall enough for its floating buttons: a tall
  // card docked above the composer can leave it a sliver.
  const [roomForButtons, setRoomForButtons] = useState(true);
  const [unreadCount, setUnreadCount] = useState(0);
  const [canScrollToPreviousUser, setCanScrollToPreviousUser] = useState(false);
  const [canScrollToNextUser, setCanScrollToNextUser] = useState(false);
  const isAtBottomRef = useRef(true);
  const lastScrollHeightRef = useRef(0);
  // What was last rendered. Scrolling to the bottom and the scroll events
  // come with every streamed update, and setting a state to the value it
  // has can still render the transcript once more: they set what changed.
  const shownRef = useRef({showScrollButton, unreadCount, canScrollToPreviousUser, canScrollToNextUser});
  shownRef.current = {showScrollButton, unreadCount, canScrollToPreviousUser, canScrollToNextUser};

  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      scrollArea?.scrollTo({top: scrollArea.scrollHeight, behavior});
      isAtBottomRef.current = true;
      if (shownRef.current.showScrollButton) setShowScrollButton(false);
      if (shownRef.current.unreadCount !== 0) setUnreadCount(0);
    },
    [scrollArea],
  );

  const scrollToPreviousUserMessage = useCallback(() => {
    if (!scrollArea) return;
    const targetTop = getPreviousUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({top: Math.max(0, targetTop - 16), behavior: "smooth"});
  }, [scrollArea]);

  const scrollToNextUserMessage = useCallback(() => {
    if (!scrollArea) return;
    const targetTop = getNextUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({top: Math.max(0, targetTop - 16), behavior: "smooth"});
  }, [scrollArea]);

  useEffect(() => {
    if (!scrollArea) return;
    let lastScrollTop = scrollArea.scrollTop;
    const handleScroll = () => {
      const {scrollTop, scrollHeight, clientHeight} = scrollArea;
      const atBottom = scrollTop + clientHeight >= scrollHeight - 32;
      // Only scrolling up leaves the bottom. The view can also end up above
      // the bottom when the area shrinks (the dock or composer grows); that
      // must not unpin it.
      if (atBottom) isAtBottomRef.current = true;
      else if (scrollTop < lastScrollTop - 2) isAtBottomRef.current = false;
      lastScrollTop = scrollTop;
      const shown = shownRef.current;
      if (shown.showScrollButton === isAtBottomRef.current) setShowScrollButton(!isAtBottomRef.current);
      if (isAtBottomRef.current && shown.unreadCount !== 0) setUnreadCount(0);
      const previous = getPreviousUserMessageTop(scrollArea) !== undefined;
      if (previous !== shown.canScrollToPreviousUser) setCanScrollToPreviousUser(previous);
      const next = getNextUserMessageTop(scrollArea) !== undefined;
      if (next !== shown.canScrollToNextUser) setCanScrollToNextUser(next);
    };
    handleScroll();
    scrollArea.addEventListener("scroll", handleScroll, {passive: true});
    return () => scrollArea.removeEventListener("scroll", handleScroll);
  }, [scrollArea, userMessageCount]);

  // Stay pinned to the bottom while content grows anywhere (older tasks
  // rendering progressively, a thinking note expanding) if the reader is
  // already there.
  useEffect(() => {
    if (!scrollArea) return;
    const content = scrollArea.firstElementChild;
    if (!content) return;
    const observer = new ResizeObserver(() => {
      if (isAtBottomRef.current) scrollArea.scrollTop = scrollArea.scrollHeight;
    });
    observer.observe(scrollArea);
    for (const child of Array.from(scrollArea.children)) observer.observe(child);
    return () => observer.disconnect();
  }, [scrollArea, layout]);

  useEffect(() => {
    if (!scrollArea) return;
    const observer = new ResizeObserver(() => setRoomForButtons(scrollArea.clientHeight >= FLOATING_BUTTONS_MIN_HEIGHT));
    observer.observe(scrollArea);
    return () => observer.disconnect();
  }, [scrollArea]);

  useLayoutEffect(() => {
    if (!scrollArea) return;
    const currentHeight = scrollArea.scrollHeight;
    const hasNewContent = currentHeight > lastScrollHeightRef.current;
    const isFirstRender = lastScrollHeightRef.current === 0;
    const isNewUserMessage = messages.at(-1)?.role === "user";
    if (hasNewContent && (isFirstRender || isAtBottomRef.current || isNewUserMessage)) {
      scrollToBottom(isFirstRender || serverStatus === "running" ? "auto" : "smooth");
    } else if (hasNewContent) {
      setUnreadCount((count) => count + 1);
    }
    lastScrollHeightRef.current = currentHeight;
  }, [content, messages, scrollArea, scrollToBottom, serverStatus]);

  return {
    showScrollButton: roomForButtons && showScrollButton,
    showTaskButtons: roomForButtons && (canScrollToPreviousUser || canScrollToNextUser),
    unreadCount,
    canScrollToPreviousUser,
    canScrollToNextUser,
    scrollToBottom,
    scrollToPreviousUserMessage,
    scrollToNextUserMessage,
  };
}
