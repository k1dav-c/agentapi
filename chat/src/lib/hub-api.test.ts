import {describe, expect, test} from "bun:test";
import {createHubAPI, HubAPIError} from "./hub-api";
import type {Fetcher} from "./hub-update";

function fetcher(...responses: (Response | Error)[]): Fetcher & {calls: {url: string; init: RequestInit}[]} {
  const calls: {url: string; init: RequestInit}[] = [];
  const fake = async (url: string, init: RequestInit) => {
    calls.push({url, init});
    const next = responses.shift() ?? new Response(null, {status: 204});
    if (next instanceof Error) throw next;
    return next;
  };
  return Object.assign(fake, {calls});
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});

describe("the hub's API", () => {
  test("changes go to the instance, the pins' order to /api/pins, with the session", async () => {
    const answers = fetcher();
    const api = createHubAPI("/hub", answers);
    await api.changeInstance("dev-1:3284", {pinned: true});
    await api.forgetInstance("dev-1:3284");
    await api.setPins(["a", "b"]);
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body])).toEqual([
      ["PATCH", "/hub/api/instances/dev-1%3A3284", '{"pinned":true}'],
      ["DELETE", "/hub/api/instances/dev-1%3A3284", undefined],
      ["PUT", "/hub/api/pins", '{"ids":["a","b"]}'],
    ]);
    expect(answers.calls.every(({init}) => init.credentials === "same-origin")).toBe(true);
  });

  test("a refusal throws, with what the hub said", async () => {
    const api = createHubAPI("", fetcher(json(404, {error: "no such instance"}), json(422, {title: "Unprocessable Entity"}), new Response("<html>", {status: 502})));
    const refused = (promise: Promise<void>) => promise.then(() => undefined, (error: unknown) => error);
    const notFound = await refused(api.forgetInstance("x"));
    expect(notFound).toBeInstanceOf(HubAPIError);
    expect(notFound).toMatchObject({message: "no such instance", status: 404});
    expect(await refused(api.changeInstance("x", {group: "g"}))).toMatchObject({message: "Unprocessable Entity", status: 422});
    expect(await refused(api.setPins([]))).toMatchObject({message: "The hub answered 502.", status: 502});
  });

  test("a hub that doesn't answer throws too", async () => {
    const api = createHubAPI("", fetcher(new TypeError("Failed to fetch")));
    await expect(api.changeInstance("x", {muted: true})).rejects.toMatchObject({message: "The hub didn't answer.", status: 0});
  });
});
