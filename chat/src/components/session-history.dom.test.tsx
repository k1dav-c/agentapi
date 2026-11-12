import {afterEach, describe, expect, test} from "bun:test";
import type {SessionCost} from "@/lib/chat-api";
import {withDOM} from "@/test/dom";
import {mount} from "@/test/render";
import {SpendCard} from "./session-history";

withDOM();

let view: Awaited<ReturnType<typeof mount>> | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
});

// Tokens that add up to n: half input, a quarter cached, a quarter output.
const tokens = (n: number) => ({input: n / 2, cache_read: n / 4, cache_write: 0, output: n / 4});

type ProviderCost = SessionCost["providers"][number];
const onProvider = (provider: string, usd: number, calls: number, n: number, priced = true): ProviderCost => ({provider, usd, calls, tokens: tokens(n), priced});

function cost(providers: ProviderCost[]): SessionCost {
  const usd = providers.reduce((sum, each) => sum + each.usd, 0);
  const calls = providers.reduce((sum, each) => sum + each.calls, 0);
  return {usd, calls, tokens: tokens(1000), hourly: [{hour: "2026-11-01T10:00:00Z", usd, tokens: tokens(1000)}], models: [], providers};
}

async function show(total: SessionCost) {
  view = await mount(<SpendCard total={total} sessions={2} />);
  return view.container;
}

const rows = (container: HTMLElement) => [...container.querySelectorAll("[data-provider-spend] li")].map((row) => row.textContent);

describe("SpendCard by provider", () => {
  test("lists each provider once the agent used one besides its own", async () => {
    const container = await show(
      cost([
        onProvider("default", 3.4, 120, 1_200_000),
        onProvider("gateway", 0, 40, 300_000, false),
        onProvider("vllm", 1.25, 2, 900, false),
        onProvider("anthropic-proxy", 0.5, 1, 5000),
      ]),
    );
    expect(container.querySelector("[data-provider-spend]")?.textContent).toStartWith("By provider");
    expect(rows(container)).toEqual([
      "Own provider120 calls · 1.2M tokens$3.40",
      // No model of its calls has a price: tokens, and no $0.00.
      "gateway40 calls · 300k tokensno price",
      // Some have one: the cost leaves the others out.
      "vllm2 calls · 900 tokens$1.25+",
      "anthropic-proxy1 call · 5k tokens$0.50",
    ]);
    expect(container.querySelector("[data-provider=gateway]")?.getAttribute("title")).toContain("Some calls were to a model without a known price");
  });

  test("hidden while every call went to the agent's own provider", async () => {
    const container = await show(cost([onProvider("default", 3.4, 120, 1_200_000)]));
    expect(container.querySelector("[data-spend-card]")).not.toBeNull();
    expect(container.querySelector("[data-provider-spend]")).toBeNull();
  });

  test("hidden for a server that doesn't split the cost by provider", async () => {
    const container = await show({...cost([]), providers: undefined} as unknown as SessionCost);
    expect(container.querySelector("[data-provider-spend]")).toBeNull();
  });
});
