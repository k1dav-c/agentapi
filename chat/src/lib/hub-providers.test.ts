import {describe, expect, test} from "bun:test";
import {createHubAPI} from "./hub-api";
import {
  changeOf,
  checkFailureText,
  draftOf,
  draftProblem,
  keptChange,
  parseModelIDs,
  parseProviders,
  providerEndpoint,
  providerHue,
  providerModelsText,
  providerMonogram,
  toggleModel,
  providerName,
  type Provider,
  type ProviderDraft,
} from "./hub-providers";
import type {Fetcher} from "./hub-update";

const gateway: Provider = {
  id: "gateway",
  name: "LiteLLM",
  base_url: "https://gateway.example.com/v1",
  models: [{id: "gpt-5", name: "GPT-5"}, {id: "claude-sonnet"}],
  has_key: true,
};

describe("parseProviders", () => {
  test("a list of providers, without what isn't one", () => {
    expect(
      parseProviders([
        {...gateway, models: [...gateway.models!, {id: ""}, "x"]},
        {id: "gateway", base_url: "https://other"},
        {id: "local", name: "", base_url: "http://localhost:4000", anthropic_base_url: "", models: []},
        {id: 3, base_url: "https://x"},
        {id: "no-url"},
        null,
      ]),
    ).toEqual([gateway, {id: "local", base_url: "http://localhost:4000", has_key: false}]);
    expect(parseProviders({})).toBeUndefined();
  });
});

describe("providerName and providerModelsText", () => {
  test("the name, else the ID; the models, or that it lists them", () => {
    expect(providerName(gateway)).toBe("LiteLLM");
    expect(providerName({id: "local", name: " "})).toBe("local");
    expect(providerModelsText(gateway)).toBe("2 models");
    expect(providerModelsText({...gateway, models: [{id: "a"}]})).toBe("1 model");
    expect(providerModelsText({...gateway, models: undefined})).toBe("lists its models");
  });
});

describe("drafts and changes", () => {
  test("a provider's draft has its models one per line, and no key", () => {
    expect(draftOf(gateway)).toEqual({
      id: "gateway",
      name: "LiteLLM",
      baseURL: "https://gateway.example.com/v1",
      anthropicBaseURL: "",
      models: "gpt-5\nclaude-sonnet",
      key: "",
      removeKey: false,
    });
    expect(draftOf()).toMatchObject({id: "", models: "", key: ""});
  });

  test("models are one per line or comma-separated, each once", () => {
    expect(parseModelIDs(" a, b\n\nc ,a,\n")).toEqual(["a", "b", "c"]);
    expect(parseModelIDs(" \n, ")).toEqual([]);
  });

  test("an unchanged draft keeps the key and the models' names", () => {
    expect(changeOf(draftOf(gateway), gateway)).toEqual({
      id: "gateway",
      name: "LiteLLM",
      base_url: "https://gateway.example.com/v1",
      models: [{id: "gpt-5", name: "GPT-5"}, {id: "claude-sonnet"}],
    });
    expect(keptChange(gateway)).toEqual(changeOf(draftOf(gateway), gateway));
  });

  test("a typed key replaces it, remove removes it, empty fields are left out", () => {
    const draft: ProviderDraft = {...draftOf(gateway), name: "  ", anthropicBaseURL: " https://anthropic.example.com ", models: "o3, gpt-5", key: " sk-new "};
    expect(changeOf(draft, gateway)).toEqual({
      id: "gateway",
      base_url: "https://gateway.example.com/v1",
      anthropic_base_url: "https://anthropic.example.com",
      models: [{id: "o3"}, {id: "gpt-5", name: "GPT-5"}],
      api_key: "sk-new",
    });
    expect(changeOf({...draft, removeKey: true}, gateway).api_key).toBe("");
    expect(changeOf({...draftOf(gateway), models: ""}, gateway).models).toBeUndefined();
  });
});

describe("draftProblem", () => {
  const draft = (change: Partial<ProviderDraft>): ProviderDraft => ({...draftOf(), id: "gw", baseURL: "https://gw.example.com/v1", ...change});
  test("the server's rules", () => {
    expect(draftProblem(draft({}), [])).toBeUndefined();
    expect(draftProblem(draft({anthropicBaseURL: "http://localhost:4000"}), ["other"])).toBeUndefined();
    expect(draftProblem(draft({id: ""}), [])).toBe("The ID is empty.");
    for (const id of ["Gw", "-gw", "g w", "a".repeat(64)]) expect(draftProblem(draft({id}), [])).toContain("lowercase letters");
    expect(draftProblem(draft({id: "a".repeat(63)}), [])).toBeUndefined();
    expect(draftProblem(draft({id: "default"}), [])).toContain("agent's own provider");
    expect(draftProblem(draft({}), ["gw"])).toBe("Another provider has the ID “gw”.");
    for (const baseURL of ["", "gw.example.com", "ftp://gw.example.com", "https://", "https:///v1"]) {
      expect(draftProblem(draft({baseURL}), [])).toBe("The base URL must be an http or https URL.");
    }
    expect(draftProblem(draft({anthropicBaseURL: "anthropic"}), [])).toBe("The Anthropic base URL must be an http or https URL.");
  });
});

describe("the hub's API", () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});
  const fetcher = (...responses: Response[]) => {
    const calls: {url: string; init: RequestInit}[] = [];
    const fake: Fetcher = async (url, init) => {
      calls.push({url, init});
      return responses.shift() ?? new Response(null, {status: 204});
    };
    return Object.assign(fake, {calls});
  };

  test("the providers and their revision come from /api/providers, and are replaced there", async () => {
    const answers = fetcher(json(200, {revision: "r1", providers: [gateway]}), json(200, {revision: "r2", providers: []}));
    const api = createHubAPI("/hub", answers);
    expect(await api.getProviders()).toEqual({revision: "r1", providers: [gateway]});
    expect(await api.setProviders("r1", [{id: "gw", base_url: "https://gw", api_key: "k"}])).toEqual({revision: "r2", providers: []});
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body])).toEqual([
      [undefined, "/hub/api/providers", undefined],
      ["PUT", "/hub/api/providers", '{"revision":"r1","providers":[{"id":"gw","base_url":"https://gw","api_key":"k"}]}'],
    ]);
  });

  test("a numeric revision stays numeric when it is sent back", async () => {
    const answers = fetcher(json(200, {revision: 7, providers: [gateway]}), json(200, {revision: 8, providers: []}));
    const api = createHubAPI("", answers);
    const current = await api.getProviders();
    expect(await api.setProviders(current.revision, [])).toEqual({revision: 8, providers: []});
    expect(JSON.parse(String(answers.calls[1].init.body)).revision).toBe(7);
  });

  test("a refusal throws with the hub's message, an incomplete answer too", async () => {
    const message = "invalid provider: id \"GW\" must be lowercase letters, digits, - and _";
    await expect(createHubAPI("", fetcher(json(400, {code: "invalid", message, error: message}))).setProviders("r1", [])).rejects.toMatchObject({message, status: 400});
    await expect(createHubAPI("", fetcher(json(200, {}))).getProviders()).rejects.toMatchObject({message: "The hub's answer has no providers."});
    await expect(createHubAPI("", fetcher(json(200, {providers: []}))).getProviders()).rejects.toMatchObject({message: "The hub's answer has no providers revision."});
  });
});

describe("provider looks", () => {
  test("endpoint", () => {
    expect(providerEndpoint("https://gateway.example.com/v1/")).toBe("gateway.example.com/v1");
    expect(providerEndpoint("http://10.0.0.12:8000/v1")).toBe("10.0.0.12:8000/v1");
    expect(providerEndpoint("not a url")).toBe("not a url");
  });
  test("monogram", () => {
    expect(providerMonogram("LiteLLM gateway")).toBe("LG");
    expect(providerMonogram("kimi")).toBe("KI");
    expect(providerMonogram("Local vLLM")).toBe("LV");
    expect(providerMonogram("—")).toBe("?");
  });
  test("hue", () => {
    expect(providerHue("gateway")).toBe(providerHue("gateway"));
    expect(providerHue("gateway")).toBeGreaterThanOrEqual(0);
    expect(providerHue("gateway")).toBeLessThan(360);
  });
  test("toggle a model", () => {
    expect(toggleModel("a\nb", "c")).toBe("a\nb\nc");
    expect(toggleModel("a, b", "a")).toBe("b");
    expect(toggleModel("", "a")).toBe("a");
  });
});

test("check failures in words", () => {
  expect(checkFailureText("GET /models answered 401 Unauthorized")).toBe("The provider refused the key.");
  expect(checkFailureText("GET /models answered 404 Not Found")).toContain("/v1");
  expect(checkFailureText("GET /models answered 500 Internal Server Error")).toBe("The provider answered 500.");
  expect(checkFailureText('Get "https://x/v1/models": dial tcp: lookup x: no such host')).toBe("The hub couldn't reach the provider.");
  expect(checkFailureText("read the models: invalid character")).toContain("not with a list");
  expect(checkFailureText("something else")).toBe("something else");
});
