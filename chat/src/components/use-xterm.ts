"use client";

import {useEffect, useRef, type RefObject} from "react";
import type {ITerminalOptions, Terminal} from "@xterm/xterm";
import {OrderedInput} from "@/lib/tty";
import {useStableCallback} from "@/lib/use-stable-callback";
import {TERMINAL_OPTIONS, prepareTerminalInput, useTerminalKeyboard} from "./terminal-chrome";

// useXterm runs an xterm.js terminal in containerRef, as both terminal
// views do: it loads xterm, opens a terminal with options(container), and
// sends what is typed to send, in order (onSendError reports a failure).
// connect wires the terminal to what it shows and returns how to stop. A
// new key makes a new terminal; the callbacks may change at any time.
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
    void import("@xterm/xterm").then(({Terminal}) => {
      if (disposed) return;
      term = new Terminal({...TERMINAL_OPTIONS, ...optionsFor(container)});
      term.open(container);
      prepareTerminalInput(container, setKeyboardOpen);
      // On a touch screen the keyboard opens when asked for (the keyboard
      // button, or a tap on the terminal), not as soon as the view opens.
      if (!window.matchMedia("(pointer: coarse)").matches) term.focus();
      const input = new OrderedInput((data) => Promise.resolve(sendData(data)), reportError);
      term.onData((data) => input.push(data));
      termRef.current = term;
      inputRef.current = input;
      disconnect = connectTo(term, container);
    });
    return () => {
      disposed = true;
      disconnect?.();
      term?.dispose();
      termRef.current = null;
      inputRef.current = null;
    };
  }, [containerRef, key, optionsFor, sendData, reportError, connectTo, setKeyboardOpen]);

  // Keep the last lines in view when the keyboard opens or closes.
  useEffect(() => {
    termRef.current?.scrollToBottom();
  }, [inset, keyboardOpen]);

  return {termRef, inputRef, ...keyboard};
}
