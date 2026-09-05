"use client";

import { useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";

interface KeyboardShortcutsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const shortcuts = [
  {
    section: "General",
    items: [
      { keys: ["⌘", "F"], label: "Search tasks" },
      { keys: ["?"], label: "Show shortcuts" },
    ],
  },
  {
    section: "Message Input",
    items: [
      { keys: ["Enter"], label: "Send task" },
      { keys: ["Shift", "Enter"], label: "New line" },
      { keys: ["Esc"], label: "Cancel edit" },
    ],
  },
  {
    section: "Terminal Mode",
    items: [
      { keys: ["Ctrl", "C"], label: "Interrupt" },
      { keys: ["Ctrl", "D"], label: "Send EOF" },
      { keys: ["Ctrl", "Z"], label: "Suspend" },
      { keys: ["Ctrl", "L"], label: "Clear screen" },
    ],
  },
];

export function useKeyboardShortcutsKey(onOpen: () => void) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // Don't trigger when typing in inputs
      const tag = (e.target as HTMLElement)?.tagName;
      const isEditable = tag === "INPUT" || tag === "TEXTAREA" ||
        (e.target as HTMLElement)?.getAttribute("role") === "textbox" ||
        (e.target as HTMLElement)?.isContentEditable;
      if (isEditable) return;
      if (e.key === "?" && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault();
        onOpen();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onOpen]);
}

export function KeyboardShortcutsDialog({ open, onOpenChange }: KeyboardShortcutsDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="space-y-5 py-2">
          {shortcuts.map((group) => (
            <div key={group.section}>
              <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                {group.section}
              </h3>
              <div className="space-y-1.5">
                {group.items.map((item) => (
                  <div
                    key={item.label}
                    className="flex items-center justify-between gap-4 text-sm"
                  >
                    <span className="text-muted-foreground">{item.label}</span>
                    <div className="flex items-center gap-1">
                      {item.keys.map((key) => (
                        <kbd
                          key={key}
                          className="inline-flex min-w-6 items-center justify-center rounded border bg-muted px-1.5 py-0.5 font-mono text-[11px] font-medium text-muted-foreground shadow-sm"
                        >
                          {key}
                        </kbd>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
