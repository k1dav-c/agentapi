"use client";

import { useEffect, useState } from "react";

// Whether a media query matches, kept in sync with the viewport. False until
// mounted, so the first render matches the static export.
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(false);
  useEffect(() => {
    const list = window.matchMedia(query);
    const update = () => setMatches(list.matches);
    update();
    list.addEventListener("change", update);
    return () => list.removeEventListener("change", update);
  }, [query]);
  return matches;
}
