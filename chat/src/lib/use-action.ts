"use client";

import {useEffect, useState} from "react";
import {getErrorMessage} from "./error-utils";

// useAction runs one action of a card's at a time: busy while it runs, and
// the message of the last one that failed until another starts. A new
// subject (another stop, another failure) starts without the last one's
// error.
export function useAction(subject?: unknown) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => setError(""), [subject]);
  const run = async (action: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (failure) {
      setError(getErrorMessage(failure));
    } finally {
      setBusy(false);
    }
  };
  return {busy, error, run};
}
