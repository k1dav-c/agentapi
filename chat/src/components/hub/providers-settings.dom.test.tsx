import {afterEach, describe, expect, mock, spyOn, test} from "bun:test";
import {act} from "react";
import {toast} from "sonner";
import type {HubInstance} from "@/lib/hub";
import {idleInstance, summary} from "@/lib/hub-fixtures";
import type {Provider} from "@/lib/hub-providers";
import {withDOM} from "@/test/dom";
import {json, restoreFetch, routeFetch, type Call} from "@/test/fetch";
import {button, click, mount, settle} from "@/test/render";
import {ProvidersSettings} from "./providers-settings";

withDOM();

const alpha: Provider = {
  id: "alpha",
  name: "Alpha",
  base_url: "https://alpha.example.com/v1",
  models: [{id: "a-old"}],
  has_key: true,
};
const bravo: Provider = {
  id: "bravo",
  name: "Bravo",
  base_url: "https://bravo.example.com/v1",
  models: [{id: "b-old"}],
  has_key: false,
};

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

async function enter(field: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const prototype = field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    // React reads an input through its native setter, as the browser sets it.
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", {bubbles: true}));
  });
}

function body(call: Call) {
  return JSON.parse(String(call.init.body)) as {revision?: string; providers?: Provider[]; base_url?: string; anthropic_base_url?: string; api_key?: string};
}

describe("ProvidersSettings saves", () => {
  test("a stale save conflicts, refreshes the list, and keeps the draft for a revised retry", async () => {
    let revision = "r1";
    let providers = [alpha, bravo];
    let writes = 0;
    const calls = routeFetch({
      "/hub/api/providers": () => json({revision, providers}),
      "PUT /hub/api/providers": (call) => {
        const sent = body(call);
        if (sent.revision !== revision) return json({message: "provider revision conflict", error: "provider revision conflict"}, 409);
        writes++;
        revision = "r3";
        providers = sent.providers!.map((provider) => ({...provider, has_key: providers.find((each) => each.id === provider.id)?.has_key ?? false}));
        return json({revision, providers});
      },
    });
    const conflictToast = spyOn(toast, "error").mockImplementation(() => "conflict");
    const container = await showProviders();
    await click(button(container, "Edit Alpha"));
    await enter(container.querySelector<HTMLInputElement>("input[aria-label='Name']")!, "Alpha local");

    // Another client saved after this page read r1.
    revision = "r2";
    providers = [{...alpha, name: "Alpha remote"}, {...bravo, name: "Bravo remote"}];
    await click(button(container, "Save"));
    await settle();
    expect(writes).toBe(0);
    expect(conflictToast).toHaveBeenCalledWith("The providers changed elsewhere", {description: expect.any(String)});
    expect(calls.filter((call) => call.init.method === undefined)).toHaveLength(2);
    expect(container.textContent).toContain("Bravo remote");
    const draft = container.querySelector("form")!;
    expect(draft.querySelector<HTMLInputElement>("input[aria-label='Name']")!.value).toBe("Alpha local");

    await click(button(draft, "Save"));
    await settle();
    expect(writes).toBe(1);
    const puts = calls.filter((call) => call.init.method === "PUT").map(body);
    expect(puts.map((put) => put.revision)).toEqual(["r1", "r2"]);
    expect(puts[1].providers).toMatchObject([{id: "alpha", name: "Alpha local"}, {id: "bravo", name: "Bravo remote"}]);
    expect(container.querySelector("form")).toBeNull();
    expect(container.textContent).toContain("Alpha local");
    expect(container.textContent).toContain("Bravo remote");
  });

  test("Alpha's deferred save doesn't close a Bravo draft opened meanwhile", async () => {
    let releaseSave!: () => void;
    const saving = new Promise<void>((resolve) => (releaseSave = resolve));
    const calls = routeFetch({
      "/hub/api/providers": () => json({revision: "r1", providers: [alpha, bravo]}),
      "PUT /hub/api/providers": async () => {
        await saving;
        return json({revision: "r2", providers: [{...alpha, name: "Alpha saved"}, bravo]});
      },
    });
    const container = await showProviders();
    await click(button(container, "Edit Alpha"));
    await enter(container.querySelector<HTMLInputElement>("input[aria-label='Name']")!, "Alpha saved");
    await click(button(container, "Save"));
    expect(calls.filter((call) => call.init.method === "PUT")).toHaveLength(1);

    await click(button(container, "Edit Bravo"));
    await enter(container.querySelector<HTMLInputElement>("input[aria-label='Name']")!, "Bravo draft");
    await enter(container.querySelector<HTMLTextAreaElement>("textarea[aria-label='Models']")!, "b-choice");
    await act(async () => {
      releaseSave();
      await saving;
    });
    await settle();

    const draft = container.querySelector("form[aria-label='Edit Bravo']");
    expect(draft).not.toBeNull();
    expect(draft!.querySelector<HTMLInputElement>("input[aria-label='Name']")!.value).toBe("Bravo draft");
    expect(draft!.querySelector<HTMLTextAreaElement>("textarea[aria-label='Models']")!.value).toBe("b-choice");
    expect(container.textContent).toContain("Alpha saved");
  });
});

describe("ProvidersSettings connection checks", () => {
  test("late answers don't describe changed fields or replace a newer check's models", async () => {
    const pending: {call: Call; answer: (response: Response) => void}[] = [];
    routeFetch({
      "/hub/api/providers": () => json({revision: "r1", providers: [alpha]}),
      "POST /hub/api/providers/check": (call) => new Promise<Response>((resolve) => pending.push({call, answer: resolve})),
    });
    const container = await showProviders();
    await click(button(container, "Edit Alpha"));
    const form = container.querySelector("form")!;
    const models = form.querySelector<HTMLTextAreaElement>("textarea[aria-label='Models']")!;
    const status = () => form.querySelector("[role='status']")?.textContent ?? "";
    await enter(models, "kept-model");

    await click(button(form, "Check connection"));
    await enter(form.querySelector<HTMLInputElement>("input[aria-label='Base URL']")!, "https://new.example.com/v1");
    await click(button(form, "Check connection"));
    expect(pending.map(({call}) => body(call).base_url)).toEqual([alpha.base_url, "https://new.example.com/v1"]);

    await act(async () => pending[1].answer(json({models: [{id: "b-new"}], latency_ms: 22})));
    await settle();
    expect(status()).toContain("Reached in 22 ms");
    await click(button(form, "b-new"));
    expect(models.value).toBe("kept-model\nb-new");
    await act(async () => pending[0].answer(json({models: [{id: "a-new"}], latency_ms: 11})));
    await settle();
    expect(status()).toContain("Reached in 22 ms");
    expect(status()).not.toContain("11 ms");
    expect(button(form, "b-new")).toBeDefined();
    expect(button(form, "a-new")).toBeUndefined();
    expect(models.value).toBe("kept-model\nb-new");

    // Editing the key invalidates a check too, without touching model picks.
    await click(button(form, "Check connection"));
    await enter(form.querySelector<HTMLInputElement>("input[aria-label='API key']")!, "new-secret");
    await act(async () => pending[2].answer(json({models: [{id: "c-new"}], latency_ms: 33})));
    await settle();
    expect(status()).toBe("");
    expect(form.querySelector("[aria-label=\"The provider's models\"]")).toBeNull();
    expect(models.value).toBe("kept-model\nb-new");

    // So does removing the kept key.
    await click(button(form, "Check connection"));
    await click(form.querySelector("input[type='checkbox']"));
    await act(async () => pending[3].answer(json({models: [{id: "d-new"}], latency_ms: 44})));
    await settle();
    expect(status()).toBe("");
    expect(models.value).toBe("kept-model\nb-new");

    // The alternate Claude endpoint is checked too, and an edit to it
    // invalidates the answer just like the main endpoint does.
    await click(button(form, "Check connection"));
    await click(button(form, "Advanced"));
    expect(body(pending[4].call).anthropic_base_url).toBe("");
    await enter(form.querySelector<HTMLInputElement>("input[aria-label='Anthropic base URL']")!, "https://claude.example.com");
    await act(async () => pending[4].answer(json({models: [{id: "e-new"}], latency_ms: 55})));
    await settle();
    expect(status()).toBe("");
    expect(form.querySelector("[aria-label=\"The provider's models\"]")).toBeNull();
    expect(models.value).toBe("kept-model\nb-new");
  });
});

describe("ProvidersSettings usage", () => {
  // n tokens and day dollars in the last 24 hours, seven times both in the
  // last 7 days.
  const onProvider = (provider: string, day: number, n: number) => ({
    provider,
    day_usd: day,
    week_usd: 7 * day,
    day_tokens: {input: n, output: 0, cache_write: 0, cache_read: 0, total: n},
    week_tokens: {input: 7 * n, output: 0, cache_write: 0, cache_read: 0, total: 7 * n},
  });
  const reporting = (id: string, scope: string, providers: ReturnType<typeof onProvider>[]): HubInstance =>
    idleInstance(id, {summary: summary({spend: {day_usd: 0, week_usd: 0, scope, updated_at: "2026-11-03T11:00:00Z", providers}})});
  const usage = (container: HTMLElement, id: string) => container.querySelector(`[data-provider="${id}"] [data-provider-usage]`)?.textContent;

  test("each provider says what the agents used it for, priced or not", async () => {
    routeFetch({"/hub/api/providers": () => json({revision: "r1", providers: [alpha, bravo]})});
    const instances = [
      reporting("a", "s1", [onProvider("default", 2, 1000), onProvider("alpha", 1.5, 2000)]),
      // The same sessions as a: counted once.
      reporting("b", "s1", [onProvider("default", 2, 1000), onProvider("alpha", 1.5, 2000)]),
    ];
    view = await mount(<ProvidersSettings root="/hub" instances={instances} editor={{current: null}} />);
    await settle();
    expect(usage(view.container, "alpha")).toBe("24h 2k tokens, $1.50 · 7d 14k tokens, $11");
    expect(usage(view.container, "bravo")).toBe("No calls in the last 7 days");

    await view.rerender(<ProvidersSettings root="/hub" instances={[reporting("c", "s2", [onProvider("bravo", 0, 500)])]} editor={{current: null}} />);
    expect(usage(view.container, "bravo")).toBe("24h 500 tokens, no price · 7d 3.5k tokens, no price");
  });

  test("no line while no agent reports its providers", async () => {
    routeFetch({"/hub/api/providers": () => json({revision: "r1", providers: [alpha]})});
    view = await mount(<ProvidersSettings root="/hub" instances={[idleInstance("a")]} editor={{current: null}} />);
    await settle();
    expect(view.container.querySelector("[data-provider=alpha]")).not.toBeNull();
    expect(view.container.querySelector("[data-provider-usage]")).toBeNull();
  });
});

describe("ProvidersSettings health", () => {
  const healthOf = (container: HTMLElement, id: string) => container.querySelector<HTMLElement>(`[data-provider="${id}"] [data-provider-health]`);
  const calling = (id: string, provider: string, requests: number, failures: number): HubInstance =>
    idleInstance(id, {
      summary: summary({
        provider_health: [
          {
            provider,
            requests,
            failures,
            client_errors: 0,
            first_byte_p50_ms: 900,
            first_byte_p95_ms: 1800,
            last_success_at: failures < requests ? new Date().toISOString() : undefined,
            last_failure: failures > 0 ? {at: new Date(Date.now() - 30_000).toISOString(), kind: "status", status: 503, message: "Overloaded"} : undefined,
          },
        ],
      }),
    });

  test("each provider the agents called says how it answered in the last hour", async () => {
    routeFetch({"/hub/api/providers": () => json({revision: "r1", providers: [alpha, bravo]})});
    view = await mount(<ProvidersSettings root="/hub" instances={[calling("a", "alpha", 30, 0), calling("b", "alpha", 10, 1)]} editor={{current: null}} />);
    await settle();
    const alphaHealth = healthOf(view.container, "alpha");
    expect(alphaHealth?.textContent).toBe("1h 40 calls · 3% failed · p50 900 ms · 503 Overloaded just now");
    expect(alphaHealth?.dataset.failing).toBeUndefined();
    expect(alphaHealth?.title).toContain("1 of 40 calls failed in the last hour");
    expect(healthOf(view.container, "bravo")).toBeNull();

    await view.rerender(<ProvidersSettings root="/hub" instances={[calling("c", "bravo", 2, 2)]} editor={{current: null}} />);
    expect(healthOf(view.container, "alpha")).toBeNull();
    const bravoHealth = healthOf(view.container, "bravo");
    expect(bravoHealth?.dataset.failing).toBe("true");
    expect(bravoHealth?.textContent).toBe("1h 2 calls · 100% failed · 503 Overloaded just now");
  });
});
