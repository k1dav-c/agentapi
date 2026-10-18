import {describe, expect, test} from "bun:test";
import {mkdtempSync, readFileSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";

// api-schema.ts is generated from openapi.json (bun run gen:api). The
// server's schema changes with `make gen`; this fails until the chat's
// types follow.
describe("the API types", () => {
  test("are generated from the current openapi.json", () => {
    const out = join(mkdtempSync(join(tmpdir(), "api-schema-")), "api-schema.ts");
    const run = Bun.spawnSync(["./node_modules/.bin/openapi-typescript", "../openapi.json", "-o", out], {
      cwd: join(import.meta.dir, "..", ".."),
      stderr: "pipe",
    });
    expect(run.exitCode).toBe(0);
    const generated = readFileSync(out, "utf8");
    const committed = readFileSync(join(import.meta.dir, "api-schema.ts"), "utf8");
    expect(committed === generated ? "up to date" : "run bun run gen:api").toBe("up to date");
  });
});
