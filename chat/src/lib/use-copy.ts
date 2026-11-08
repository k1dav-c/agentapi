"use client";

import {useState} from "react";
import {copyText} from "./clipboard";

// useCopy copies text to the clipboard (copyText) and says, for a moment
// (ms), that it did: copied, for a button that shows a check. copy
// resolves to whether it could; the clipboard can be blocked, as it is in
// some frames.
export function useCopy(ms = 1600) {
  const [copied, setCopied] = useState(false);
  const copy = async (text: string): Promise<boolean> => {
    try {
      await copyText(text);
    } catch {
      setCopied(false);
      return false;
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), ms);
    return true;
  };
  return {copied, copy};
}
