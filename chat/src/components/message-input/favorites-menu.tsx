"use client";

import {useEffect, useRef, useState} from "react";
import {Search, Settings, Star} from "lucide-react";
import {Button} from "@/components/ui/button";
import {DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import {favoriteName, favoritePreview, favoritesSearchAfter, filterFavorites} from "@/lib/hub-favorites";
import {manageFavorites, useFrameFavorites} from "../hub/frame-favorites";

// FavoritesMenu is ☆ in the composer of a chat the hub opens: the favorites
// kept on the hub, which onInsert puts in the message, and Manage
// favorites…, which opens them in the hub's settings. Nothing in a chat
// opened without the hub, or by a hub that doesn't send them. Past
// favoritesSearchAfter of them, a search box filters the list; typing in
// the list types in it.
export function FavoritesMenu({onInsert}: {onInsert: (text: string) => void}) {
  const favorites = useFrameFavorites();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  const content = useRef<HTMLDivElement>(null);
  // A favorite was picked: the composer keeps the focus, not ☆.
  const inserted = useRef(false);
  // Manage favorites… was picked: the hub opens its settings once the menu
  // is gone. A menu still on its way out takes the focus back from the
  // hub's dialog as the dialog covers the pointer.
  const manage = useRef(false);

  const searchable = (favorites?.length ?? 0) > favoritesSearchAfter;
  // The search box takes the focus where a keyboard is at hand; a phone's
  // would cover the list.
  useEffect(() => {
    if (!open || !searchable || !window.matchMedia("(pointer: fine)").matches) return;
    const frame = requestAnimationFrame(() => search.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [open, searchable]);

  if (!favorites) return null;
  const shown = searchable ? filterFavorites(favorites, query) : favorites;
  const firstItem = () => content.current?.querySelector<HTMLElement>("[role=menuitem]");

  return (
    <DropdownMenu
      modal={false}
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery("");
      }}
    >
      <DropdownMenuTrigger asChild>
        <Button type="button" size="icon" variant="ghost" className="size-10 shrink-0 rounded-full text-muted-foreground hover:text-foreground" title="Favorites">
          <Star />
          <span className="sr-only">Favorites</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        ref={content}
        side="top"
        align="end"
        collisionPadding={8}
        data-favorites-menu
        className="flex max-h-[min(28rem,var(--radix-dropdown-menu-content-available-height))] w-[min(22rem,calc(100vw-2rem))] flex-col overflow-hidden p-0"
        onCloseAutoFocus={(event) => {
          if (manage.current) {
            manage.current = false;
            event.preventDefault();
            manageFavorites();
          } else if (inserted.current) {
            inserted.current = false;
            event.preventDefault();
          }
        }}
        onKeyDown={(event) => {
          // Typing in the list types in the search box (the menu would jump
          // to the item that starts with the letter).
          const input = search.current;
          if (!input || event.target === input || event.ctrlKey || event.metaKey || event.altKey) return;
          if (event.key.length === 1 || event.key === "Backspace") {
            event.preventDefault();
            input.focus();
            setQuery((previous) => (event.key === "Backspace" ? previous.slice(0, -1) : previous + event.key));
          }
        }}
      >
        <DropdownMenuLabel className="px-3 pt-2.5 pb-1 text-xs font-medium text-muted-foreground">Insert a favorite</DropdownMenuLabel>
        {searchable && (
          <div className="relative px-2 pb-1.5">
            <Search className="pointer-events-none absolute top-1/2 left-4.5 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <input
              ref={search}
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search favorites"
              aria-label="Search favorites"
              className="h-9 w-full rounded-lg border bg-muted/50 pr-2 pl-7.5 text-sm outline-none placeholder:text-muted-foreground/80 focus-visible:border-ring focus-visible:bg-background [&::-webkit-search-cancel-button]:hidden"
              onKeyDown={(event) => {
                if (event.key === "ArrowDown") {
                  event.preventDefault();
                  firstItem()?.focus();
                } else if (event.key === "Enter") {
                  event.preventDefault();
                  firstItem()?.click();
                } else if (event.key !== "Escape" && event.key !== "Tab") {
                  // The input's own keys, not the menu's.
                  event.stopPropagation();
                }
              }}
            />
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-1 pb-1">
          {favorites.length === 0 && (
            <p className="px-2 py-3 text-sm text-muted-foreground">No favorites yet. Keep the prompts you send often on the hub, and insert them here.</p>
          )}
          {favorites.length > 0 && shown.length === 0 && <p className="px-2 py-3 text-sm text-muted-foreground">Nothing matches “{query.trim()}”.</p>}
          {shown.map((favorite) => {
            const preview = favoritePreview(favorite);
            return (
              <DropdownMenuItem
                key={favorite.id}
                className="min-h-10 flex-col items-stretch gap-0.5 py-2"
                textValue={favoriteName(favorite)}
                onSelect={() => {
                  inserted.current = true;
                  onInsert(favorite.text);
                }}
              >
                <span className="truncate font-medium">{favoriteName(favorite)}</span>
                {preview && <span className="truncate text-xs text-muted-foreground">{preview}</span>}
              </DropdownMenuItem>
            );
          })}
        </div>
        <DropdownMenuSeparator className="mx-0 my-0" />
        <div className="p-1">
          <DropdownMenuItem
            className="min-h-10"
            onSelect={() => {
              manage.current = true;
            }}
          >
            <Settings /> Manage favorites…
          </DropdownMenuItem>
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
