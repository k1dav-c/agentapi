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

  test("the favorites go to /api/favorites, and come back as the hub keeps them", async () => {
    const answers = fetcher(json(200, {favorites: [{id: "f1", text: "Review the diff."}, {id: 7, text: "not one"}]}));
    const api = createHubAPI("/hub", answers);
    expect(await api.setFavorites([{text: "Review the diff."}])).toEqual([{id: "f1", text: "Review the diff."}]);
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body])).toEqual([["PUT", "/hub/api/favorites", '{"favorites":[{"text":"Review the diff."}]}']]);
    await expect(createHubAPI("", fetcher(json(400, {error: "favorite 1: the text is empty"}))).setFavorites([{text: ""}])).rejects.toMatchObject({
      message: "favorite 1: the text is empty",
      status: 400,
    });
  });

  test("forgetting the stopped ones posts their IDs, and returns those the hub forgot", async () => {
    const answers = fetcher(json(200, {ids: ["dev-1:3284"]}));
    expect(await createHubAPI("/hub", answers).forgetStopped(["dev-1:3284", "dev-2:3284"])).toEqual(["dev-1:3284"]);
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body])).toEqual([["POST", "/hub/api/instances/forget", '{"ids":["dev-1:3284","dev-2:3284"]}']]);
    await expect(createHubAPI("", fetcher(json(200, {}))).forgetStopped(["x"])).rejects.toMatchObject({message: "The hub's answer doesn't say which it forgot."});
    await expect(createHubAPI("", fetcher(json(400, {error: "send ids"}))).forgetStopped([])).rejects.toMatchObject({message: "send ids", status: 400});
  });

  test("signing in posts the token, with no session yet; a wrong one is a 401", async () => {
    const answers = fetcher(new Response(null, {status: 204}), json(401, {code: "unauthorized", message: "invalid token", error: "invalid token"}));
    const api = createHubAPI("/hub", answers);
    await api.signIn("secret");
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body, init.credentials])).toEqual([["POST", "/hub/api/session", '{"token":"secret"}', "same-origin"]]);
    await expect(api.signIn("wrong")).rejects.toMatchObject({status: 401});
  });

  test("adding one by its URL returns its ID, or what the hub said", async () => {
    const answers = fetcher(json(201, {id: "dev-1:3284", url: "http://dev-1:3284"}));
    expect(await createHubAPI("/hub", answers).addInstance({url: "http://dev-1:3284", token: "t"})).toBe("dev-1:3284");
    expect(answers.calls.map(({url, init}) => [init.method, url, init.body])).toEqual([["POST", "/hub/api/instances", '{"url":"http://dev-1:3284","token":"t"}']]);
    await expect(createHubAPI("", fetcher(json(400, {code: "bad_request", message: "the URL isn't http or https", error: "the URL isn't http or https"}))).addInstance({url: "x"})).rejects.toMatchObject({
      message: "the URL isn't http or https",
      status: 400,
    });
    await expect(createHubAPI("", fetcher(json(201, {}))).addInstance({url: "http://x"})).rejects.toMatchObject({message: "The hub's answer doesn't say which instance it added."});
  });
});
