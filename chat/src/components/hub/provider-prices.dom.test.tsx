import {afterEach, describe, expect, mock, spyOn, test} from "bun:test";
import {act} from "react";
import {toast} from "sonner";
import type {PriceStatus, Provider, ProviderChange} from "@/lib/hub-providers";
import {withDOM} from "@/test/dom";
import {json, restoreFetch, routeFetch, type Call} from "@/test/fetch";
import {button, click, mount, settle} from "@/test/render";
import {ProvidersSettings} from "./providers-settings";

withDOM();

const k3 = {input: 0.192, output: 16, cache_read: 0.182, cache_write: 0};
const batch = {input: 2.28, output: 11.4, cache_read: 0.228, cache_write: 0};
const gateway: Provider = {
  id: "gateway",
  name: "LiteLLM",
  base_url: "https://gateway.example.com/v1",
  models: [{id: "Kimi-K3-NVFP4"}, {id: "local-model"}],
  has_key: true,
  model_prices: [
    {model: "Kimi-K3-NVFP4", choice: "auto", openrouter: "moonshotai/kimi-k3", openrouter_name: "MoonshotAI: Kimi K3", price: {...k3, source: "openrouter:moonshotai/kimi-k3"}},
    {model: "local-model", choice: "auto"},
  ],
};
const other: Provider = {id: "other", base_url: "https://other.example.com/v1", has_key: false, openrouter: {x: "none"}, model_prices: [{model: "x", choice: "none"}]};
const prices: PriceStatus = {enabled: true, fetched_at: "2026-11-14T05:00:00Z", models: 463};

let view: Awaited<ReturnType<typeof mount>> | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
  restoreFetch();
  mock.restore();
});

async function showProviders() {
  view = await mount(<ProvidersSettings root="/hub" editor={{current: null}} />);
  await settle();
  return view.container;
}

async function enter(field: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", {bubbles: true}));
  });
}

const sent = (call: Call) => JSON.parse(String(call.init.body)) as {revision: string; providers: ProviderChange[]};
const rows = (container: HTMLElement) =>
  [...container.querySelectorAll("[data-provider=gateway] [data-model-price]")].map((row) => row.textContent?.replace(/Change$/, ""));

// A hub that keeps what it is sent, and prices a picked model from its list.
function fakeHub() {
  let revision = 1;
  let providers = [gateway, other];
  const answer = () => json({revision: String(revision), providers, prices});
  const calls = routeFetch({
    "/hub/api/providers": answer,
    "PUT /hub/api/providers": (call) => {
      const body = sent(call);
      if (body.revision !== String(revision)) return json({message: "conflict", error: "conflict"}, 409);
      revision++;
      providers = providers.map((p) => {
        const change = body.providers.find((each) => each.id === p.id)!;
        const choice = change.openrouter?.["Kimi-K3-NVFP4"];
        if (p.id !== "gateway") return {...p, openrouter: change.openrouter};
        const [first, second] = p.model_prices!;
        const kimi =
          choice === "moonshotai/kimi-k3:batch"
            ? {model: first.model, choice: "chosen" as const, openrouter: choice, openrouter_name: "MoonshotAI: Kimi K3 (batch)", price: {...batch, source: `openrouter:${choice}`}}
            : choice === "none"
              ? {model: first.model, choice: "none" as const}
              : gateway.model_prices![0];
        return {...p, openrouter: change.openrouter, model_prices: [kimi, second]};
      });
      return answer();
    },
    "/hub/api/providers/openrouter": (call) => {
      const q = new URL(call.url, "http://hub.test").searchParams.get("q");
      const models = [
        {id: "moonshotai/kimi-k3", name: "MoonshotAI: Kimi K3", price: k3},
        {id: "moonshotai/kimi-k3:batch", name: "MoonshotAI: Kimi K3 (batch)", price: batch},
      ].filter((m) => q?.split(" ").every((word) => m.id.includes(word)));
      return json({models, fetched_at: prices.fetched_at});
    },
  });
  return calls;
}

describe("ProvidersSettings prices", () => {
  test("each model shows its price and where it comes from, folded under the provider", async () => {
    fakeHub();
    const container = await showProviders();
    const toggle = button(container.querySelector("[data-provider=gateway]")!, /^Prices · 1 of 2 models$/)!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(rows(container)).toEqual([]);
    await click(toggle);
    expect(rows(container)).toEqual(["Kimi-K3-NVFP4$0.19 / $16 per M tokens · OpenRouter: Kimi K3auto", "local-modelNo price · No OpenRouter model of this name"]);
    expect(container.querySelector("[data-model-price='Kimi-K3-NVFP4'] [title]")?.getAttribute("title")).toBe(
      "USD per million tokens: input $0.19, output $16, cache read $0.18",
    );
    expect(container.querySelector("[data-prices-note]")?.textContent).toContain("OpenRouter's list prices");
  });

  test("a pick from the searched list saves it, the other providers as they are; No price and Automatic too", async () => {
    const calls = fakeHub();
    const container = await showProviders();
    await click(button(container.querySelector("[data-provider=gateway]")!, /^Prices/));
    await click(button(container, "Change the price of Kimi-K3-NVFP4"));
    await settle();
    const search = container.querySelector<HTMLInputElement>("input[aria-label='Search OpenRouter models']")!;
    expect(search.value).toBe("kimi-k3");
    await enter(search, "kimi-k3 batch");
    await settle();
    const options = [...container.querySelectorAll("[role=option]")];
    expect(options.map((o) => o.textContent)).toEqual(["Kimi K3 (batch) moonshotai/kimi-k3:batch$2.28 / $11.4 per M tokens"]);
    await click(options[0]);
    await settle();
    const puts = calls.filter((call) => call.init.method === "PUT").map(sent);
    expect(puts).toHaveLength(1);
    expect(puts[0].revision).toBe("1");
    expect(puts[0].providers).toEqual([
      {id: "gateway", name: "LiteLLM", base_url: gateway.base_url, models: gateway.models, openrouter: {"Kimi-K3-NVFP4": "moonshotai/kimi-k3:batch"}},
      {id: "other", base_url: other.base_url, openrouter: {x: "none"}},
    ]);
    expect(container.querySelector("[role=listbox]")).toBeNull();
    expect(rows(container)[0]).toBe("Kimi-K3-NVFP4$2.28 / $11.4 per M tokens · OpenRouter: Kimi K3 (batch)picked");

    await click(button(container, "Change the price of Kimi-K3-NVFP4"));
    await settle();
    await click(button(container, "No price"));
    await settle();
    expect(rows(container)[0]).toBe("Kimi-K3-NVFP4No price · No price, as picked");
    await click(button(container, "Change the price of Kimi-K3-NVFP4"));
    await settle();
    expect(button(container, "No price")).toBeUndefined();
    await click(button(container, "Automatic"));
    await settle();
    const last = calls.filter((call) => call.init.method === "PUT").map(sent);
    expect(last.map((put) => put.providers[0].openrouter)).toEqual([{"Kimi-K3-NVFP4": "moonshotai/kimi-k3:batch"}, {"Kimi-K3-NVFP4": "none"}, {}]);
    expect(rows(container)[0]).toContain("OpenRouter: Kimi K3auto");
  });

  test("a stale pick conflicts and reads the list again, keeping the picker open", async () => {
    let revision = "r1";
    const calls = routeFetch({
      "/hub/api/providers": () => json({revision, providers: [gateway], prices}),
      "PUT /hub/api/providers": () => json({message: "conflict", error: "conflict"}, 409),
      "/hub/api/providers/openrouter": () => json({models: []}),
    });
    const conflict = spyOn(toast, "error").mockImplementation(() => "conflict");
    const container = await showProviders();
    await click(button(container, /^Prices/));
    await click(button(container, "Change the price of local-model"));
    await settle();
    expect(container.textContent).toContain("No OpenRouter model has these words");
    revision = "r2";
    await click(button(container, "No price"));
    await settle();
    expect(conflict).toHaveBeenCalledWith("The providers changed elsewhere", {description: expect.any(String)});
    expect(calls.filter((call) => !call.init.method && call.url === "/hub/api/providers")).toHaveLength(2);
    expect(container.querySelector("[aria-label='Price of local-model']")).not.toBeNull();
  });

  test("Refresh prices asks the hub, and says when OpenRouter failed", async () => {
    let refreshed = 0;
    const listed: Provider = {id: "lister", base_url: "https://lister.example.com/v1", has_key: true};
    const calls = routeFetch({
      "/hub/api/providers": () => json({revision: "r1", providers: [listed], prices: {enabled: true, models: 0}}),
      "POST /hub/api/providers/prices": () => {
        refreshed++;
        return json({
          revision: "r1",
          providers: [{...listed, model_prices: [{model: "Kimi-K3-NVFP4", choice: "auto"}]}],
          prices: {enabled: true, models: 0, error: "OpenRouter answered 502 Bad Gateway"},
        });
      },
    });
    const failed = spyOn(toast, "error").mockImplementation(() => "failed");
    const container = await showProviders();
    expect(container.textContent).toContain("Prices · refresh them to list its models");
    expect(container.querySelector("[data-prices-note]")?.textContent).toContain("once the hub has them");
    await click(button(container, "Refresh prices"));
    await settle();
    expect(refreshed).toBe(1);
    expect(calls.at(-1)?.init.method).toBe("POST");
    expect(failed).toHaveBeenCalledWith("Couldn't get OpenRouter's prices", {description: expect.stringContaining("502")});
    expect(container.textContent).toContain("Prices · 0 of 1 model");
    expect(container.querySelector("[data-prices-note]")?.textContent).toContain("The last refresh failed");
  });

  test("a hub that doesn't look prices up shows none of it", async () => {
    routeFetch({"/hub/api/providers": () => json({revision: "r1", providers: [{id: "old", base_url: "https://old.example.com/v1", has_key: false}]})});
    const container = await showProviders();
    expect(container.querySelector("[data-provider=old]")).not.toBeNull();
    expect(button(container, "Refresh prices")).toBeUndefined();
    expect(container.querySelector("[data-provider-prices]")).toBeNull();
    expect(container.querySelector("[data-prices-note]")).toBeNull();
  });
});
