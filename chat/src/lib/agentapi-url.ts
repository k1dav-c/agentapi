// The URL of the AgentAPI server the chat talks to: the url query
// parameter, or else the server the chat is served from, found from the
// chat's base path. undefined when there is neither.
export function resolveAgentAPIUrl(
  paramsUrl: string | null,
  basePath: string | undefined = process.env.NEXT_PUBLIC_BASE_PATH,
  origin?: string,
): string | undefined {
  if (paramsUrl) return paramsUrl;
  if (!basePath) return undefined;
  // Read only here: the url parameter must work where there is no window
  // (the development server renders the page on the server first).
  origin ??= window.location.origin;
  // NOTE(cian): We use '../' here to construct the agent API URL relative
  // to the chat's location. Let's say the app is hosted on a subpath
  // `/@admin/workspace.agent/apps/ccw/`. When you visit this URL you get
  // redirected to `/@admin/workspace.agent/apps/ccw/chat/embed`. This serves
  // this React application, but it needs to know where the agent API is hosted.
  // This will be at the root of where the application is mounted e.g.
  // `/@admin/workspace.agent/apps/ccw/`. Previously we used
  // `window.location.origin` but this assumes that the application owns the
  // entire origin.
  // See: https://github.com/coder/coder/issues/18779#issuecomment-3133290494 for more context.
  let chatURL: string = new URL(basePath, origin).toString();
  // NOTE: trailing slashes and relative URLs are tricky.
  // https://developer.mozilla.org/en-US/docs/Web/API/URL_API/Resolving_relative_references#current_directory_relative
  if (!chatURL.endsWith("/")) {
    chatURL += "/";
  }
  const agentAPIURL = new URL("..", chatURL).toString();
  if (agentAPIURL.endsWith("/")) {
    return agentAPIURL.slice(0, -1);
  }
  return agentAPIURL;
}
