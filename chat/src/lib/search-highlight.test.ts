import {describe, expect, test} from "bun:test";
import {splitByQuery} from "./search-highlight";

describe("search highlight", () => {
  test("splits around every match, ignoring case", () => {
    expect(splitByQuery("Run go test, then GO build", "go")).toEqual([
      {text: "Run ", match: false},
      {text: "go", match: true},
      {text: " test, then ", match: false},
      {text: "GO", match: true},
      {text: " build", match: false},
    ]);
  });

  test("a query is text, not a pattern", () => {
    expect(splitByQuery("a.b and axb", "a.b")).toEqual([
      {text: "a.b", match: true},
      {text: " and axb", match: false},
    ]);
  });

  test("no query is one part", () => {
    expect(splitByQuery("text", "  ")).toEqual([{text: "text", match: false}]);
  });
});
