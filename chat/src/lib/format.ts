// Counts, amounts and ages as the chat and the hub say them: one way each,
// rounded the same everywhere. The bounds are where rounding would reach
// the next unit: 999,600 tokens is 1M, not 1000k.

const units: [number, string][] = [
  [1e3, "k"],
  [1e6, "M"],
  [1e9, "B"],
  [1e12, "T"],
];

// compact is a count of 1,000 or more in its unit: one decimal below 10
// ("1.2k", "1k"), none from 10 on ("12k").
function compact(n: number): string {
  for (const [i, [unit, suffix]] of units.entries()) {
    const scaled = n / unit;
    if (scaled < 999.5 || i === units.length - 1) return `${scaled.toFixed(scaled < 9.95 ? 1 : 0).replace(/\.0$/, "")}${suffix}`;
  }
  return String(n);
}

// formatTokens is a token count at a glance: "950", "1.2k", "12k", "3.4M",
// "1.2B".
export function formatTokens(tokens: number): string {
  const n = Math.max(0, Math.round(tokens));
  return n < 1000 ? String(n) : compact(n);
}

// formatUSD is an amount to the cent: "$12.34", "$0.004" below a cent,
// "$1,234" from a thousand on.
export function formatUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(3)}`;
  if (usd >= 999.995) return `$${Math.round(usd).toLocaleString("en-US")}`;
  return `$${usd.toFixed(2)}`;
}

// formatUSDShort is an amount at a glance, for the hub's list: "$0",
// "$4.20", "$110", "$1.2k", "$1.5M".
export function formatUSDShort(usd: number): string {
  if (usd < 0.005) return "$0";
  if (usd < 9.995) return `$${usd.toFixed(2)}`;
  if (usd < 999.5) return `$${Math.round(usd)}`;
  return `$${compact(usd)}`;
}

// ageSeconds is how many whole seconds ago at (a timestamp or a time in
// milliseconds) was; none when it doesn't parse. A time ahead is now.
function ageSeconds(at: string | number | undefined, now: number): number | undefined {
  if (at === undefined || at === "") return undefined;
  const time = typeof at === "number" ? at : Date.parse(at);
  if (Number.isNaN(time)) return undefined;
  return Math.max(0, Math.floor((now - time) / 1000));
}

// formatAgo is how long ago at was, in words: "just now", "12 min ago",
// "3 h ago", "2 d ago"; "" when it doesn't say.
export function formatAgo(at: string | number | undefined, now: number): string {
  const seconds = ageSeconds(at, now);
  if (seconds === undefined) return "";
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} h ago`;
  return `${Math.floor(seconds / 86400)} d ago`;
}

// formatAge is how long ago at was, short, for the hub's rows: "now",
// "45s", "12m", "3h", "2d"; "" when it doesn't say.
export function formatAge(at: string | number | undefined, now: number): string {
  const seconds = ageSeconds(at, now);
  if (seconds === undefined) return "";
  if (seconds < 10) return "now";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}
