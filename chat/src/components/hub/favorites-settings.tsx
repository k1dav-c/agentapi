"use client";

import {useEffect, useLayoutEffect, useMemo, useRef, useState} from "react";
import {ArrowDown, ArrowUp, Pencil, Plus, Star, Trash2} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {createHubAPI, hubErrorText} from "@/lib/hub-api";
import {
  favoriteName,
  favoritePreview,
  maxFavorites,
  maxFavoriteText,
  maxFavoriteTitle,
  moveFavorite,
  type Favorite,
} from "@/lib/hub-favorites";
import {cn} from "@/lib/utils";
import {fieldClass} from "./dialogs";

// newFavoriteID is the ID of a favorite added here: the hub keeps the IDs it
// is sent, so the list keeps its keys while it saves. getRandomValues works
// on a hub reached over plain HTTP; randomUUID doesn't.
function newFavoriteID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(8));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

// What the editor edits: a favorite by its ID, or a new one.
type Editing = {id: string; isNew: boolean};

// FavoritesSettings is the Favorites section of the hub's settings: the
// prompts kept on the hub, which the chat of an agent opened here inserts in
// its composer. Each change saves the whole list at once (PUT
// /api/favorites); the list shows it before the hub answers, and goes back
// to the hub's with a toast when the hub refuses it. editor tells the
// dialog whether the editor is open, and closes it: Escape closes the
// editor first, and a click outside doesn't lose what it holds.
export function FavoritesSettings({
  root,
  favorites,
  onSaved,
  editor,
  headingRef,
}: {
  root: string;
  favorites: Favorite[] | undefined;
  onSaved: (favorites: Favorite[]) => void;
  editor: React.RefObject<{open: boolean; cancel: () => void} | null>;
  headingRef?: React.Ref<HTMLHeadingElement>;
}) {
  const api = useMemo(() => createHubAPI(root), [root]);
  const [pending, setPending] = useState<Favorite[] | null>(null);
  const [editing, setEditing] = useState<Editing | null>(null);
  const shown = useMemo(() => pending ?? favorites ?? [], [pending, favorites]);
  const latest = useRef(shown);
  latest.current = shown;

  // One save at a time, in order: each sends the whole list.
  const chain = useRef<Promise<void>>(Promise.resolve());
  const inFlight = useRef(0);
  const save = (next: Favorite[]) => {
    setPending(next);
    inFlight.current++;
    chain.current = chain.current.then(async () => {
      try {
        onSaved(await api.setFavorites(next));
      } catch (error) {
        toast.error("Couldn't save the favorites", {description: hubErrorText(error)});
      } finally {
        inFlight.current--;
        if (inFlight.current === 0) setPending(null);
      }
    });
  };

  useEffect(() => {
    editor.current = {open: editing !== null, cancel: () => setEditing(null)};
  });

  // A moved favorite keeps the focus on the button that moved it, or on
  // the other one once it reached an end.
  const moved = useRef<{id: string; step: -1 | 1} | null>(null);
  const order = shown.map((favorite) => favorite.id).join("\n");
  useLayoutEffect(() => {
    const focus = moved.current;
    if (!focus) return;
    moved.current = null;
    const button = (step: number) => document.querySelector<HTMLButtonElement>(`[data-favorite-move="${CSS.escape(focus.id)}:${step}"]`);
    const same = button(focus.step);
    (same && !same.disabled ? same : button(-focus.step))?.focus();
  }, [order]);

  const move = (index: number, step: -1 | 1) => {
    const next = moveFavorite(latest.current, index, step);
    if (next === latest.current) return;
    moved.current = {id: latest.current[index].id, step};
    save([...next]);
  };

  // The favorite deleted last, for Undo: a toast would sit under the
  // dialog, out of its reach.
  const [deleted, setDeleted] = useState<{favorite: Favorite; index: number} | null>(null);
  const undo = useRef<HTMLButtonElement>(null);
  const remove = (favorite: Favorite) => {
    const index = latest.current.findIndex((each) => each.id === favorite.id);
    if (index === -1) return;
    save(latest.current.filter((each) => each.id !== favorite.id));
    setDeleted({favorite, index});
    // The row with the focus is gone: Undo takes it.
    requestAnimationFrame(() => undo.current?.focus());
  };
  const restore = () => {
    if (!deleted) return;
    const list = latest.current.filter((each) => each.id !== deleted.favorite.id);
    save([...list.slice(0, deleted.index), deleted.favorite, ...list.slice(deleted.index)].slice(0, maxFavorites));
    setDeleted(null);
  };

  const commit = (favorite: Favorite) => {
    const list = latest.current;
    const index = list.findIndex((each) => each.id === favorite.id);
    save(index === -1 ? [...list, favorite] : list.map((each) => (each.id === favorite.id ? favorite : each)));
    setEditing(null);
  };

  const full = shown.length >= maxFavorites;
  const adding = editing?.isNew ? editing : null;
  return (
    <section aria-labelledby="hub-settings-favorites" className="space-y-3">
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <h3 id="hub-settings-favorites" ref={headingRef} tabIndex={-1} className="flex items-baseline gap-2 text-base font-semibold outline-none">
            Favorites
            {shown.length > 0 && (
              <span className="text-xs font-normal text-muted-foreground tabular-nums">
                {shown.length}/{maxFavorites}
              </span>
            )}
          </h3>
          <p className="text-sm text-muted-foreground">
            Prompts you send often. In the chat of an agent you open here, <Star className="inline size-3.5 -translate-y-px" aria-label="Favorites" /> inserts one in the message.
          </p>
        </div>
        {shown.length > 0 && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-9 shrink-0"
            disabled={full || !!adding}
            title={full ? `The hub keeps at most ${maxFavorites}` : undefined}
            onClick={() => setEditing({id: newFavoriteID(), isNew: true})}
          >
            <Plus /> Add
          </Button>
        )}
      </div>

      {favorites === undefined && <p className="text-sm text-muted-foreground">Loading…</p>}

      {deleted && (
        <div role="status" className="flex items-center gap-2 rounded-lg bg-muted/60 py-1 pr-1 pl-3 text-sm">
          <span className="min-w-0 flex-1 truncate">Deleted “{favoriteName(deleted.favorite)}”</span>
          <Button ref={undo} type="button" variant="ghost" size="sm" className="h-8 shrink-0" onClick={restore}>
            Undo
          </Button>
        </div>
      )}

      {favorites !== undefined && shown.length === 0 && !adding && (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed px-6 py-8 text-center">
          <Star className="size-6 text-muted-foreground" aria-hidden="true" />
          <p className="text-sm font-medium">No favorites yet</p>
          <p className="max-w-sm text-sm text-muted-foreground">They are kept on the hub, so your phone and desktop have the same ones.</p>
          <Button type="button" size="sm" className="mt-2 h-9" onClick={() => setEditing({id: newFavoriteID(), isNew: true})}>
            <Plus /> Add a favorite
          </Button>
        </div>
      )}

      {shown.length > 0 && (
        <ol aria-label="Favorites" className="divide-y overflow-hidden rounded-xl border bg-card">
          {shown.map((favorite, index) =>
            editing?.id === favorite.id ? (
              <li key={favorite.id} className="p-3">
                <FavoriteEditor favorite={favorite} onSave={commit} onCancel={() => setEditing(null)} />
              </li>
            ) : (
              <li key={favorite.id} className="flex items-center gap-1 py-1.5 pr-1.5 pl-3" data-favorite={favorite.id}>
                <div className="min-w-0 flex-1 py-1">
                  <p className="truncate text-sm font-medium">{favoriteName(favorite)}</p>
                  {favoritePreview(favorite) && <p className="truncate text-xs text-muted-foreground">{favoritePreview(favorite)}</p>}
                </div>
                <div className="flex shrink-0 items-center">
                  <IconButton
                    label={`Move ${favoriteName(favorite)} up`}
                    disabled={index === 0}
                    data-favorite-move={`${favorite.id}:-1`}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp />
                  </IconButton>
                  <IconButton
                    label={`Move ${favoriteName(favorite)} down`}
                    disabled={index === shown.length - 1}
                    data-favorite-move={`${favorite.id}:1`}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown />
                  </IconButton>
                  <IconButton label={`Edit ${favoriteName(favorite)}`} onClick={() => setEditing({id: favorite.id, isNew: false})}>
                    <Pencil />
                  </IconButton>
                  <IconButton label={`Delete ${favoriteName(favorite)}`} className="hover:text-destructive" onClick={() => remove(favorite)}>
                    <Trash2 />
                  </IconButton>
                </div>
              </li>
            ),
          )}
        </ol>
      )}

      {adding && (
        <div className="rounded-xl border bg-card p-3">
          <FavoriteEditor favorite={{id: adding.id, text: ""}} onSave={commit} onCancel={() => setEditing(null)} />
        </div>
      )}
    </section>
  );
}

function IconButton({label, className, ...props}: React.ComponentProps<typeof Button> & {label: string}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={label}
      title={label}
      className={cn("size-9 rounded-full text-muted-foreground hover:text-foreground disabled:opacity-30", className)}
      {...props}
    />
  );
}

// FavoriteEditor edits a favorite's title (optional: the first line names
// it without one) and text. ⌘/Ctrl+Enter saves.
function FavoriteEditor({favorite, onSave, onCancel}: {favorite: Favorite; onSave: (favorite: Favorite) => void; onCancel: () => void}) {
  const [title, setTitle] = useState(favorite.title ?? "");
  const [text, setText] = useState(favorite.text);
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => {
    form.current?.scrollIntoView({block: "nearest"});
  }, []);
  const ready = text.trim() !== "";
  const submit = () => {
    if (!ready) return;
    const name = title.trim().replace(/\s+/g, " ");
    onSave(name ? {id: favorite.id, title: name, text} : {id: favorite.id, text});
  };
  const isNew = favorite.text === "";
  return (
    <form
      ref={form}
      aria-label={isNew ? "New favorite" : `Edit ${favoriteName(favorite)}`}
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
          event.preventDefault();
          submit();
        }
      }}
    >
      <label className="block space-y-1.5">
        <span className="flex items-baseline justify-between text-sm font-medium">
          Title
          <span className="text-xs font-normal text-muted-foreground">Optional · the first line without one</span>
        </span>
        <input
          autoFocus
          value={title}
          maxLength={maxFavoriteTitle}
          onChange={(event) => setTitle(event.target.value)}
          placeholder={favoriteName({text}) || "Review the diff"}
          aria-label="Title"
          className={fieldClass}
        />
      </label>
      <label className="block space-y-1.5">
        <span className="flex items-baseline justify-between text-sm font-medium">
          Prompt
          {text.length > maxFavoriteText * 0.8 && (
            <span className="text-xs font-normal text-muted-foreground tabular-nums">
              {text.length}/{maxFavoriteText}
            </span>
          )}
        </span>
        <textarea
          value={text}
          maxLength={maxFavoriteText}
          onChange={(event) => setText(event.target.value)}
          rows={5}
          placeholder={"Review the diff on this branch.\nList the risks before the nits."}
          aria-label="Prompt"
          className={cn(fieldClass, "h-auto min-h-28 resize-y py-2.5 leading-6")}
        />
      </label>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" className="h-9" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" className="h-9" disabled={!ready}>
          {isNew ? "Add" : "Save"}
        </Button>
      </div>
    </form>
  );
}
