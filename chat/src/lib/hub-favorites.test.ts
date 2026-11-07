import {describe, expect, test} from "bun:test";
import {favoriteInsertion, favoriteName, favoritePreview, filterFavorites, moveFavorite, parseFavorites} from "./hub-favorites";

describe("favoriteName and favoritePreview", () => {
  test("a titled favorite: its title, and its text on one line", () => {
    const favorite = {id: "a", title: "Review", text: "Review the diff.\n\n  List the risks."};
    expect(favoriteName(favorite)).toBe("Review");
    expect(favoritePreview(favorite)).toBe("Review the diff. List the risks.");
  });

  test("without a title: the first line that isn't blank, and the rest", () => {
    const favorite = {id: "a", title: " ", text: "\n  Review the diff.\nList the risks.\n"};
    expect(favoriteName(favorite)).toBe("Review the diff.");
    expect(favoritePreview(favorite)).toBe("List the risks.");
    expect(favoritePreview({id: "b", text: "One line"})).toBe("");
  });
});

describe("filterFavorites", () => {
  const list = [
    {id: "a", title: "Review", text: "Review the diff."},
    {id: "b", text: "Write the TESTS first."},
  ];
  test("every word, in the title or the text, in any case", () => {
    expect(filterFavorites(list, "")).toEqual(list);
    expect(filterFavorites(list, "tests")).toEqual([list[1]]);
    expect(filterFavorites(list, "review diff")).toEqual([list[0]]);
    expect(filterFavorites(list, "review tests")).toEqual([]);
  });
});

describe("favoriteInsertion", () => {
  const apply = (draft: string, text: string) => {
    const {from, insert} = favoriteInsertion(draft, text);
    return draft.slice(0, from) + insert;
  };
  test("an empty draft, or one of spaces, becomes the text", () => {
    expect(favoriteInsertion("", "Do it.")).toEqual({from: 0, insert: "Do it."});
    expect(apply("  \n ", "Do it.")).toBe("Do it.");
  });
  test("otherwise the text is a paragraph after the draft", () => {
    expect(apply("Look at main.go", "Do it.")).toBe("Look at main.go\n\nDo it.");
    expect(apply("Look at main.go  ", "Do it.")).toBe("Look at main.go\n\nDo it.");
    expect(apply("Look at main.go\n", "Do it.")).toBe("Look at main.go\n\nDo it.");
    expect(apply("Look at main.go\n\n", "Do it.")).toBe("Look at main.go\n\nDo it.");
  });
});

describe("parseFavorites", () => {
  test("a list of favorites, without what isn't one", () => {
    expect(parseFavorites([{id: "a", title: "", text: "x"}, {id: "b", title: 3, text: "y"}, {id: "c", text: ""}])).toEqual([{id: "a", text: "x"}]);
    expect(parseFavorites({})).toBeUndefined();
    expect(parseFavorites(Array.from({length: 120}, (_, n) => ({id: `f${n}`, text: "x"})))).toHaveLength(100);
  });
});

describe("moveFavorite", () => {
  test("one place up or down, the same list at an end", () => {
    const list = ["a", "b", "c"];
    expect(moveFavorite(list, 1, -1)).toEqual(["b", "a", "c"]);
    expect(moveFavorite(list, 1, 1)).toEqual(["a", "c", "b"]);
    expect(moveFavorite(list, 0, -1)).toBe(list);
    expect(moveFavorite(list, 2, 1)).toBe(list);
  });
});
