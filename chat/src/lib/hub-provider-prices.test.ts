import {describe, expect, test} from "bun:test";
import {createHubAPI} from "./hub-api";
import {
  keptChange,
  modelSearchText,
  openRouterName,
  parseProviders,
  perMillion,
  priceDetails,
  pricedCount,
  priceSourceText,
  priceText,
  withPriceChoice,
  type Provider,
  type ProviderModelPrice,
} from "./hub-providers";
import type {Fetcher} from "./hub-update";

const kimi: ProviderModelPrice = {
  model: "Kimi-K3-NVFP4",
  choice: "auto",
  openrouter: "moonshotai/kimi-k3",
  openrouter_name: "MoonshotAI: Kimi K3",
  price: {input: 0.192, output: 16, cache_read: 0.182, cache_write: 0, source: "openrouter:moonshotai/kimi-k3"},
};

const gateway: Provider = {
  id: "gateway",
  base_url: "https://gateway.example.com/v1",
  models: [{id: "Kimi-K3-NVFP4"}, {id: "local"}],
  has_key: true,
  openrouter: {local: "none"},
  model_prices: [kimi, {model: "local", choice: "none"}],
};

describe("provider prices", () => {
  test("a provider's prices and choices are read, without what isn't one", () => {
    expect(
      parseProviders([
        {
          ...gateway,
          openrouter: {local: "none", "": "x", bad: 3},
          model_prices: [...gateway.model_prices!, {model: "x", choice: "maybe"}, {choice: "auto"}, {model: "y", choice: "auto", price: {input: "1"}}],
        },
      ]),
    ).toEqual([{...gateway, model_prices: [kimi, {model: "local", choice: "none"}, {model: "y", choice: "auto"}]}]);
    // A hub before the prices.
    expect(parseProviders([{id: "old", base_url: "https://old"}])).toEqual([{id: "old", base_url: "https://old", has_key: false}]);
  });

  test("prices per million tokens, short", () => {
    expect(perMillion(16)).toBe("$16");
    expect(perMillion(2.5)).toBe("$2.5");
    expect(perMillion(2.28)).toBe("$2.28");
    expect(perMillion(0.192)).toBe("$0.19");
    expect(perMillion(0.05)).toBe("$0.05");
    expect(perMillion(0.0425)).toBe("$0.043");
    expect(perMillion(0)).toBe("$0");
    expect(priceText(kimi.price!)).toBe("$0.19 / $16 per M tokens");
    expect(priceDetails(kimi.price!)).toBe("USD per million tokens: input $0.19, output $16, cache read $0.18");
  });

  test("where a price comes from", () => {
    expect(openRouterName(kimi)).toBe("Kimi K3");
    expect(openRouterName({openrouter: "a/b"})).toBe("a/b");
    expect(priceSourceText(kimi)).toBe("OpenRouter: Kimi K3");
    expect(priceSourceText({model: "m", choice: "auto"})).toBe("No OpenRouter model of this name");
    expect(priceSourceText({model: "m", choice: "none"})).toBe("No price, as picked");
    expect(priceSourceText({...kimi, choice: "chosen"})).toBe("OpenRouter: Kimi K3");
    expect(priceSourceText({model: "m", choice: "chosen", openrouter: "a/gone"})).toBe("OpenRouter: a/gone isn't in the hub's list");
  });

  test("a change of one model's price keeps the rest of the provider", () => {
    const rest = {id: gateway.id, base_url: gateway.base_url, models: gateway.models, openrouter: gateway.openrouter};
    expect(keptChange(gateway)).toEqual(rest);
    expect(withPriceChoice(gateway, "Kimi-K3-NVFP4", "moonshotai/kimi-k3:batch")).toEqual({
      ...rest,
      openrouter: {local: "none", "Kimi-K3-NVFP4": "moonshotai/kimi-k3:batch"},
    });
    // Back to automatic: the last choice removed sends none.
    expect(withPriceChoice(gateway, "local", undefined)).toEqual({...rest, openrouter: {}});
    expect(gateway.openrouter).toEqual({local: "none"});
  });
});

describe("the hub's price calls", () => {
  const json = (body: unknown) => new Response(JSON.stringify(body), {status: 200, headers: {"Content-Type": "application/json"}});
  const fetcher = (...responses: Response[]) => {
    const calls: {url: string; init: RequestInit}[] = [];
    const fake: Fetcher = async (url, init) => {
      calls.push({url, init});
      return responses.shift() ?? new Response(null, {status: 204});
    };
    return Object.assign(fake, {calls});
  };

  test("refresh answers the providers with where the prices stand; search the models", async () => {
    const prices = {enabled: true, fetched_at: "2026-11-14T05:00:00Z", models: 463};
    const answers = fetcher(
      json({revision: "r1", providers: [gateway], prices}),
      json({models: [{id: "moonshotai/kimi-k3", name: "MoonshotAI: Kimi K3", price: kimi.price}, {id: "bad"}], fetched_at: prices.fetched_at}),
      json({revision: "r1", providers: []}),
    );
    const api = createHubAPI("/hub", answers);
    expect(await api.refreshPrices()).toEqual({revision: "r1", providers: [gateway], prices});
    expect(await api.searchOpenRouter("kimi k3")).toEqual([{id: "moonshotai/kimi-k3", name: "MoonshotAI: Kimi K3", price: kimi.price!}]);
    expect(await api.getProviders()).toEqual({revision: "r1", providers: []});
    expect(answers.calls.map(({url, init}) => [init.method, url])).toEqual([
      ["POST", "/hub/api/providers/prices"],
      [undefined, "/hub/api/providers/openrouter?q=kimi%20k3"],
      [undefined, "/hub/api/providers"],
    ]);
  });
});

test("the picker searches a model by the name the hub matches", () => {
  expect(modelSearchText("Kimi-K3-NVFP4")).toBe("kimi-k3");
  expect(modelSearchText("hf/Qwen4-32B_AWQ")).toBe("qwen4-32b");
  expect(modelSearchText("model-int4-gptq")).toBe("model");
  expect(modelSearchText("fp8")).toBe("fp8");
  expect(pricedCount(gateway)).toBe(1);
});
