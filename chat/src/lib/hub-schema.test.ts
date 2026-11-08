import {describe, expect, test} from "bun:test";
import {readFileSync} from "node:fs";
import {join} from "node:path";
import {hubSchemaTS} from "../../scripts/hub-schema";

// hub-schema.ts is generated from lib/hub/hub-schema.json (bun run
// gen:api). The hub's schema changes with `make gen`; this fails until the
// page's types follow.
describe("the hub's types", () => {
  test("are generated from the current hub-schema.json", async () => {
    const committed = readFileSync(join(import.meta.dir, "hub-schema.ts"), "utf8");
    expect(committed === (await hubSchemaTS()) ? "up to date" : "run bun run gen:api").toBe("up to date");
  });
});
