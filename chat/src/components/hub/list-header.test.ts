import {describe, expect, test} from "bun:test";
import {hubLabel} from "./list-header";

describe("hubLabel", () => {
  test("a release by its version, any other build as dev", () => {
    expect(hubLabel("3.12.1")).toBe("v3.12.1");
    expect(hubLabel("0.0.0-dev")).toBe("dev");
    expect(hubLabel("3.12.1-2-gabc1234-dirty")).toBe("dev");
  });
});
