// Favorites are prompts kept on the hub (GET and PUT /api/favorites): the
// hub's settings manage them, and the chat of an agent the hub opens
// inserts one in its composer. The hub tells the chat in its frame of them
// (lib/hub-frame.ts).

export type Favorite = {
  id: string;
  // Optional: without one the text's first line names it.
  title?: string;
  text: string;
};

// A favorite being added has no ID until the hub gives it one.
export type FavoriteDraft = Omit<Favorite, "id"> & {id?: string};

// The hub's limits.
export const maxFavorites = 100;
export const maxFavoriteTitle = 100;
export const maxFavoriteText = 20_000;

// The chat's menu of favorites gets a search box past this many.
export const favoritesSearchAfter = 7;

// favoriteName is what a list shows of a favorite: its title, else its
// text's first line that isn't blank.
export function favoriteName(favorite: FavoriteDraft): string {
  const title = favorite.title?.trim();
  if (title) return title;
  return (
    favorite.text
      .split("\n")
      .map((line) => line.trim())
      .find(Boolean) ?? ""
  );
}

// favoritePreview is the text on one line, for under the name: without the
// line that names it when it has no title.
export function favoritePreview(favorite: FavoriteDraft): string {
  let text = favorite.text.trim();
  if (!favorite.title?.trim()) {
    const end = text.indexOf("\n");
    text = end === -1 ? "" : text.slice(end + 1);
  }
  return text.replace(/\s+/g, " ").trim();
}

// filterFavorites keeps the favorites whose title or text has every word of
// query, in any case.
export function filterFavorites<T extends FavoriteDraft>(favorites: readonly T[], query: string): T[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...favorites];
  return favorites.filter((favorite) => {
    const haystack = `${favorite.title ?? ""}\n${favorite.text}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}

// favoriteInsertion is how a favorite's text joins the draft: what replaces
// the draft from index from. An empty draft (or one of only spaces) becomes
// the text; otherwise the text starts a paragraph of its own, after the
// draft without its trailing spaces.
export function favoriteInsertion(draft: string, text: string): {from: number; insert: string} {
  if (!draft.trim()) return {from: 0, insert: text};
  const kept = draft.replace(/[ \t]+$/, "");
  const separator = kept.endsWith("\n\n") ? "" : kept.endsWith("\n") ? "\n" : "\n\n";
  return {from: kept.length, insert: separator + text};
}

// parseFavorites reads the favorites of a message or an answer: undefined
// when it isn't a list; entries that aren't favorites are left out.
export function parseFavorites(data: unknown): Favorite[] | undefined {
  if (!Array.isArray(data)) return undefined;
  const favorites: Favorite[] = [];
  const ids = new Set<string>();
  for (const entry of data.slice(0, maxFavorites)) {
    if (!entry || typeof entry !== "object") continue;
    const {id, title, text} = entry as {id?: unknown; title?: unknown; text?: unknown};
    if (typeof id !== "string" || !id || ids.has(id) || typeof text !== "string" || !text) continue;
    if (title !== undefined && typeof title !== "string") continue;
    ids.add(id);
    favorites.push(title ? {id, title, text} : {id, text});
  }
  return favorites;
}

// moveFavorite moves the favorite at index one place up (-1) or down (1);
// the same list when it can't.
export function moveFavorite<T>(list: readonly T[], index: number, step: -1 | 1): readonly T[] {
  const to = index + step;
  if (index < 0 || index >= list.length || to < 0 || to >= list.length) return list;
  const next = [...list];
  [next[index], next[to]] = [next[to], next[index]];
  return next;
}
