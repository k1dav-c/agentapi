"use client";

import {useEffect} from "react";

// The tab icon: the hub's mark, with a dot in the most pressing state and
// the number that need you.
export function faviconUrl(waiting: number, working: boolean): string {
  const dot = waiting > 0 ? "#c27c0e" : working ? "#4f6bed" : "";
  const badge = dot
    ? `<circle cx="24" cy="8" r="7.5" fill="${dot}" stroke="#ffffff" stroke-width="1.5"/>` +
      (waiting > 0 ? `<text x="24" y="11.4" font-family="system-ui,sans-serif" font-size="10" font-weight="700" fill="#ffffff" text-anchor="middle">${waiting > 9 ? "9+" : waiting}</text>` : "")
    : "";
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="9" fill="#232733"/>` +
    `<circle cx="16" cy="16" r="3.6" fill="#ffffff"/><circle cx="9.3" cy="9.3" r="2.2" fill="#c27c0e"/><circle cx="24.6" cy="20" r="2.2" fill="#4f6bed"/><circle cx="12" cy="25" r="2.2" fill="#16a07a"/>` +
    badge +
    `</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

// useHubTab titles the tab with the number of agents that need you, and
// marks its icon (faviconUrl).
export function useHubTab(waiting: number, working: boolean) {
  useEffect(() => {
    document.title = waiting > 0 ? `(${waiting}) AgentAPI Hub` : "AgentAPI Hub";
    let link = document.querySelector<HTMLLinkElement>("link[data-hub-icon]");
    if (!link) {
      document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]').forEach((icon) => icon.remove());
      link = document.createElement("link");
      link.rel = "icon";
      link.type = "image/svg+xml";
      link.setAttribute("data-hub-icon", "");
      document.head.appendChild(link);
    }
    link.href = faviconUrl(waiting, working);
  }, [waiting, working]);
}
