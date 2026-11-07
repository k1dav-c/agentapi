"use client";

import {useRef} from "react";
import {Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import type {Favorite} from "@/lib/hub-favorites";
import {FavoritesSettings} from "./favorites-settings";

// The sections of the hub's settings, each a block of the dialog; opening it
// at one shows that one.
export type SettingsSection = "favorites";

// HubSettings is the dialog of the settings kept on the hub, which every
// browser signed in to it shares: for now the favorites. section is the one
// it opens at; null is closed.
export function HubSettings({
  root,
  section,
  onClose,
  favorites,
  onFavoritesSaved,
}: {
  root: string;
  section: SettingsSection | null;
  onClose: () => void;
  favorites: Favorite[] | undefined;
  onFavoritesSaved: (favorites: Favorite[]) => void;
}) {
  const editor = useRef<{open: boolean; cancel: () => void} | null>(null);
  const headings = useRef<Partial<Record<SettingsSection, HTMLHeadingElement | null>>>({});
  return (
    <Dialog open={section !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-xl"
        onOpenAutoFocus={(event) => {
          const heading = section ? headings.current[section] : null;
          if (!heading) return;
          event.preventDefault();
          heading.scrollIntoView({block: "start"});
          heading.focus({preventScroll: true});
        }}
        // Escape closes the editor first, then the dialog; a click outside
        // leaves an open editor be.
        onEscapeKeyDown={(event) => {
          if (!editor.current?.open) return;
          event.preventDefault();
          editor.current.cancel();
        }}
        onInteractOutside={(event) => {
          if (editor.current?.open) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>Hub settings</DialogTitle>
          <DialogDescription>Kept on the hub: every browser signed in to it has the same.</DialogDescription>
        </DialogHeader>
        <FavoritesSettings
          root={root}
          favorites={favorites}
          onSaved={onFavoritesSaved}
          editor={editor}
          headingRef={(heading) => {
            headings.current.favorites = heading;
          }}
        />
      </DialogContent>
    </Dialog>
  );
}
