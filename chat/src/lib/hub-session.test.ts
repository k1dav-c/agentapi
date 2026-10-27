import {describe, expect, test} from "bun:test";
import {checkHubSession} from "./hub-session";

const answer = (response: Response | Error): typeof fetch =>
  (async () => {
    if (response instanceof Error) throw response;
    return response;
  }) as unknown as typeof fetch;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});

describe("checkHubSession", () => {
  test("signed in or not, as the hub says", async () => {
    expect(await checkHubSession("/hub", answer(json({authenticated: true})))).toBe("signed-in");
    expect(await checkHubSession("/hub", answer(json({authenticated: false})))).toBe("signed-out");
    expect(await checkHubSession("/hub", answer(json({error: "sign in to the hub"}, 401)))).toBe("signed-out");
  });

  test("a proxy's error page or no answer is unreachable, not a sign-in", async () => {
    expect(await checkHubSession("/hub", answer(new Response("<html>502 Bad Gateway</html>", {status: 502})))).toBe("unreachable");
    expect(await checkHubSession("/hub", answer(new Response("<html>captive portal</html>", {status: 200})))).toBe("unreachable");
    expect(await checkHubSession("/hub", answer(new TypeError("Failed to fetch")))).toBe("unreachable");
  });
});
