// Writes the hub page's types (src/lib/hub-schema.ts, or the file given)
// from the hub's schema, lib/hub/hub-schema.json, which make gen writes:
// its types as openapi-typescript makes them, and hubLimits, the values of
// its HubLimits, so the page checks what the hub checks without its own
// copy of the numbers. bun run gen:api runs it; a test fails until the
// committed file matches.

import {readFileSync, writeFileSync} from "node:fs";
import {join} from "node:path";
import openapiTS, {astToString} from "openapi-typescript";

const chat = join(import.meta.dir, "..");

export async function hubSchemaTS(): Promise<string> {
  const schema = JSON.parse(readFileSync(join(chat, "..", "lib", "hub", "hub-schema.json"), "utf8"));
  const types = astToString(await openapiTS(schema));
  const limits = schema.components.schemas.HubLimits.properties as Record<string, {const: number}>;
  const values = Object.entries(limits)
    .map(([name, limit]) => `  ${name}: ${JSON.stringify(limit.const)},`)
    .join("\n");
  return `/**
 * Generated from lib/hub/hub-schema.json by scripts/hub-schema.ts (bun run
 * gen:api). Do not make direct changes to the file.
 */

${types}
// hubLimits are the values of HubLimits: the limits of what the hub takes.
export const hubLimits = {
${values}
} as const satisfies components["schemas"]["HubLimits"];
`;
}

if (import.meta.main) {
  writeFileSync(process.argv[2] ?? join(chat, "src", "lib", "hub-schema.ts"), await hubSchemaTS());
}
