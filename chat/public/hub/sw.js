// The hub page's service worker: it shows the notifications the hub pushes
// (lib/hub/push.go) while the page is closed, and opens the agent of the
// one clicked. Its scope is the hub's page; URLs here are relative to it.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

// Safari stops pushing to a page that gets a push without showing a
// notification, so there it shows one even to someone looking: WebKit's,
// which on an iPhone are every browser and the Home Screen's apps, whose
// user agent doesn't say Safari.
const mustShow = /AppleWebKit/.test(self.navigator.userAgent) && !/Chrome|Chromium|Android/.test(self.navigator.userAgent);

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = {};
  }
  event.waitUntil(show(data));
});

async function show(data) {
  if (data.kind !== "test" && !mustShow) {
    // Someone looking at the hub's page sees it there (an agent's chat in
    // a tab of its own is no hub page).
    if ((await hubPages()).some((client) => client.focused && client.visibilityState === "visible")) return;
  }
  await self.registration.showNotification(data.title || "Tendyard Hub", {
    body: data.body || "",
    tag: data.tag || "agentapi-hub",
    renotify: true,
    icon: "icon-192.png",
    data: {id: typeof data.id === "string" ? data.id : ""},
  });
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const id = event.notification.data?.id || "";
  const url = new URL(self.registration.scope);
  if (id) url.hash = encodeURIComponent(id);
  event.waitUntil(open(url, id));
});

// open focuses the hub's page and has it open the agent, or opens the page
// at the agent.
async function open(url, id) {
  const page = (await hubPages())[0];
  if (page) {
    await page.focus();
    if (id) page.postMessage({type: "agentapi-hub-open", id});
    return;
  }
  await self.clients.openWindow(url.href);
}

// hubPages are the windows of the hub's page.
async function hubPages() {
  const windows = await self.clients.matchAll({type: "window", includeUncontrolled: true});
  return windows.filter((client) => client.url.startsWith(self.registration.scope));
}
