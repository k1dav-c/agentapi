import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {FavoritesSettings} from "./favorites-settings";

const render = (favorites: Parameters<typeof FavoritesSettings>[0]["favorites"]) =>
  renderToStaticMarkup(<FavoritesSettings root="" favorites={favorites} onSaved={() => {}} editor={{current: null}} />);

describe("FavoritesSettings", () => {
  test("each favorite by its name, with its text on one line and its buttons", () => {
    const html = render([
      {id: "a", title: "Review", text: "Review the diff.\nList the risks."},
      {id: "b", text: "Write the tests first.\nThen the code."},
    ]);
    expect(html).toContain("2/100");
    expect(html).toContain("Review the diff. List the risks.");
    // No title: the first line names it, the rest is its preview.
    expect(html).toContain(">Write the tests first.</p>");
    expect(html).toContain(">Then the code.</p>");
    expect(html).toMatch(/aria-label="Move Review up"[^>]*disabled=""|disabled=""[^>]*aria-label="Move Review up"/);
    expect(html).toMatch(/aria-label="Move Write the tests first. down"[^>]*disabled=""|disabled=""[^>]*aria-label="Move Write the tests first. down"/);
    expect(html).toContain('aria-label="Edit Review"');
    expect(html).toContain('aria-label="Delete Write the tests first."');
  });

  test("none yet: how to add the first", () => {
    const html = render([]);
    expect(html).toContain("No favorites yet");
    expect(html).toContain("Add a favorite");
    expect(html).not.toContain("<ol");
  });

  test("before the hub sent them", () => {
    expect(render(undefined)).toContain("Loading…");
  });
});
