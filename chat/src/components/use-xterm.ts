"use client";

import {useEffect, useRef, useState, type RefObject} from "react";
import type {ITerminalOptions, Terminal} from "@xterm/xterm";
import {OrderedInput} from "@/lib/tty";
import {withModifier, type StickyModifier} from "@/lib/terminal-keys";
import {useStableCallback} from "@/lib/use-stable-callback";
import {TERMINAL_OPTIONS, prepareTerminalInput, useTerminalKeyboard} from "./terminal-chrome";
import {latestLink, linkProvider} from "./terminal-links";

// useXterm runs an xterm.js terminal in containerRef, as both terminal
// views do: it loads xterm, opens a terminal with options(container), and
// sends what is typed to send, in order (onSendError reports a failure).
// connect wires the terminal to what it shows and returns how to stop. A
// new key makes a new terminal; the callbacks may change at any time. Web
// links in the terminal open on a click, and link is the one it printed
// last.
export function useXterm({
  containerRef,
  key,
  options,
  send,
  onSendError,
  connect,
}: {
  containerRef: RefObject<HTMLDivElement | null>;
  key: string;
  options: (container: HTMLDivElement) => ITerminalOptions;
  send: (data: string) => Promise<void> | void;
  onSendError: (error: unknown) => void;
  connect: (term: Terminal, container: HTMLDivElement) => () => void;
}) {
  const termRef = useRef<Terminal | null>(null);
  const [link, setLink] = useState<string | null>(null);
  // Ctrl or Alt held from the key bar for the next key typed.
  const [modifier, setModifierState] = useState<StickyModifier | null>(null);
  const modifierRef = useRef<StickyModifier | null>(null);
  const setModifier = useStableCallback((value: StickyModifier | null) => {
    modifierRef.current = value;
    setModifierState(value);
  });
  const inputRef = useRef<OrderedInput | null>(null);
  const keyboard = useTerminalKeyboard();
  const {setKeyboardOpen, inset, keyboardOpen} = keyboard;
  const optionsFor = useStableCallback(options);
  const sendData = useStableCallback(send);
  const reportError = useStableCallback(onSendError);
  const connectTo = useStableCallback(connect);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    let term: Terminal | undefined;
    let disconnect: (() => void) | undefined;
    let linkTimer = 0;
    setLink(null);
    void import("@xterm/xterm").then(({Terminal}) => {
      if (disposed) return;
      term = new Terminal({...TERMINAL_OPTIONS, ...optionsFor(container)});
      term.open(container);
      const opened = term;
      opened.registerLinkProvider(linkProvider(opened));
      // Look for a new link once the output settles, not on every write.
      opened.onWriteParsed(() => {
        window.clearTimeout(linkTimer);
        linkTimer = window.setTimeout(() => {
          const found = latestLink(opened);
          if (found) setLink(found);
        }, 200);
      });
      prepareTerminalInput(container, setKeyboardOpen);
      // On a touch screen the keyboard opens when asked for (the keyboard
      // button, or a tap on the terminal), not as soon as the view opens.
      if (!window.matchMedia("(pointer: coarse)").matches) term.focus();
      const input = new OrderedInput((data) => Promise.resolve(sendData(data)), reportError);
      term.onData((data) => {
        const held = modifierRef.current;
        if (held) setModifier(null);
        input.push(held ? withModifier(data, held) : data);
      });
      termRef.current = term;
      inputRef.current = input;
      disconnect = connectTo(term, container);
    });
    return () => {
      disposed = true;
      window.clearTimeout(linkTimer);
      disconnect?.();
      term?.dispose();
      termRef.current = null;
      inputRef.current = null;
    };
  }, [containerRef, key, optionsFor, sendData, reportError, connectTo, setKeyboardOpen, setModifier]);

  // Keep the last lines in view when the keyboard opens or closes.
  useEffect(() => {
    termRef.current?.scrollToBottom();
  }, [inset, keyboardOpen]);

  return {termRef, inputRef, link, modifier, setModifier, ...keyboard};
}
