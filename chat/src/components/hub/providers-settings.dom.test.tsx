import {afterEach, describe, expect, mock, spyOn, test} from "bun:test";
import {act} from "react";
import {toast} from "sonner";
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
