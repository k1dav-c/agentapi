/**
 * Compares lib/pricing/prices.json, the built-in model prices, with Pi's model registry, and with
 * --write brings the table up to date with it.
 *
 * The table holds Anthropic's and OpenAI's list prices in USD per million tokens. Most come from the
 * registry of the Pi coding agent (@earendil-works/pi-ai, providers/data/{anthropic,openai,
 * openai-codex}.json), which tracks the providers' pricing pages. The rest were added by hand from
 * the providers' own pages, and are kept as they are:
 *
 *   - retired models Pi no longer lists, whose calls are still in old session logs: claude-opus-4,
 *     claude-opus-4-1, claude-sonnet-4, claude-3-5-haiku (platform.claude.com/docs/en/about-claude/
 *     pricing), codex-mini-latest, gpt-5-codex, gpt-5.1-codex, gpt-5.1-codex-max,
 *     gpt-5.1-codex-mini, gpt-5.2-codex (developers.openai.com/api/docs/models/<id>);
 *   - the long-context tier of claude-sonnet-4 and claude-sonnet-4-5 (1M context beta, retired
 *     2026-04-30): $6 / $22.50 above 200K input tokens (claude.com/blog/1m-context), the cache
 *     at the standard multipliers of that input (1.25x written, 0.1x read).
 *
 * A model Pi lists takes Pi's prices; its tiers stay when Pi has none. Run with the path of Pi's
 * pi-ai package (or its providers/data directory):
 *
 *   bun scripts/prices.mjs <pi-ai>            # what differs, and exits 1 if anything does
 *   bun scripts/prices.mjs <pi-ai> --write    # rewrite prices.json
 *
 * Check what --write changed against the pricing pages before committing it.
 */
import {existsSync, readFileSync, writeFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const args = process.argv.slice(2);
const write = args.includes('--write');
const source = args.find(a => !a.startsWith('--'));
if (!source) {
  console.error('usage: bun scripts/prices.mjs <path of @earendil-works/pi-ai> [--write]');
  process.exit(2);
}
const dataDir = [source, path.join(source, 'dist', 'providers', 'data'), path.join(source, 'providers', 'data')]
  .find(dir => existsSync(path.join(dir, 'anthropic.json')));
if (!dataDir) {
  console.error(`no providers/data/anthropic.json under ${source}`);
  process.exit(2);
}

const tablePath = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'lib', 'pricing', 'prices.json');
const table = JSON.parse(readFileSync(tablePath, 'utf8'));

// Pi's cost of a model in the table's format.
const price = cost => {
  const p = {input: cost.input, output: cost.output, cache_read: cost.cacheRead ?? 0, cache_write: cost.cacheWrite ?? 0};
  if (cost.tiers?.length) {
    p.tiers = cost.tiers.map(t => ({input_tokens_above: t.inputTokensAbove, ...price(t)}));
  }
  return p;
};

const registry = {};
for (const file of ['anthropic.json', 'openai.json', 'openai-codex.json']) {
  const full = path.join(dataDir, file);
  if (!existsSync(full)) continue;
  for (const models of Object.values(JSON.parse(readFileSync(full, 'utf8')))) {
    for (const m of Object.values(models)) {
      if (m.type === 'chat' && m.cost && !(m.id.toLowerCase() in registry)) {
        registry[m.id.toLowerCase()] = price(m.cost);
      }
    }
  }
}

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
let changes = 0;
for (const [id, p] of Object.entries(registry).sort()) {
  const now = table[id];
  const next = now?.tiers && !p.tiers ? {...p, tiers: now.tiers} : p;
  if (now && same(now, next)) continue;
  changes++;
  console.log(now ? `changed ${id}: ${JSON.stringify(now)} -> ${JSON.stringify(next)}` : `new     ${id}: ${JSON.stringify(next)}`);
  table[id] = next;
}
for (const id of Object.keys(table).sort()) {
  if (!(id in registry)) console.log(`kept    ${id} (not in Pi's registry)`);
}

if (write) {
  // One model a line, in order, prices with a decimal point: as the file has them.
  const num = n => (Number.isInteger(n) ? n.toFixed(1) : String(n));
  const fmt = p => {
    const parts = [];
    if (p.input_tokens_above !== undefined) parts.push(`"input_tokens_above": ${p.input_tokens_above}`);
    for (const k of ['input', 'output', 'cache_read', 'cache_write']) parts.push(`"${k}": ${num(p[k])}`);
    if (p.tiers) parts.push(`"tiers": [${p.tiers.map(fmt).join(', ')}]`);
    return `{${parts.join(', ')}}`;
  };
  const lines = Object.keys(table).sort().map(id => `  ${JSON.stringify(id)}: ${fmt(table[id])}`);
  writeFileSync(tablePath, `{\n${lines.join(',\n')}\n}\n`);
  console.log(`wrote ${tablePath}`);
} else if (changes > 0) {
  process.exit(1);
}
