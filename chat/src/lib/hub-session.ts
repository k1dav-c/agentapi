// Whether this browser is signed in to the hub (GET /api/session). A hub
// that doesn't answer, or a proxy before it that answers for it (a 502
// page), is unreachable: the page says so and tries again, rather than ask
// for the token as if the session had ended.
export type HubSession = "signed-in" | "signed-out" | "unreachable";

export async function checkHubSession(root: string, fetcher: typeof fetch = fetch): Promise<HubSession> {
  try {
    const response = await fetcher(`${root}/api/session`, {credentials: "same-origin"});
    if (response.status === 401) return "signed-out";
    if (!response.ok) return "unreachable";
    const body = (await response.json()) as {authenticated?: unknown};
    return body.authenticated === true ? "signed-in" : "signed-out";
  } catch {
    return "unreachable";
  }
}
