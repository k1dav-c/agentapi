// What the server reports in GET /status and status_change while the agent
// can't work until someone logs it in (native transport). The login happens
// in a terminal on the machine AgentAPI runs on, not in the browser.
export interface LoginRequired {
  agent: string;
  message: string;
  // The command to run in a terminal, e.g. "claude auth login".
  command: string;
  // More help, as Markdown.
  detail?: string;
  // The machine and the user AgentAPI runs as: where to run the command.
  host?: string;
  user?: string;
}

// The login the server reports, or null for anything that isn't one.
export function parseLoginRequired(value: unknown): LoginRequired | null {
  if (!value || typeof value !== "object") return null;
  const login = value as Partial<LoginRequired>;
  if (typeof login.command !== "string" || !login.command) return null;
  const text = (field: unknown) => (typeof field === "string" ? field : "");
  return {
    agent: text(login.agent),
    message: text(login.message),
    command: login.command,
    detail: text(login.detail) || undefined,
    host: text(login.host) || undefined,
    user: text(login.user) || undefined,
  };
}

export function loginTitle(login: LoginRequired): string {
  return `${login.agent || "The agent"} needs you to log in`;
}

// "on <host> as <user>": where to run the command.
export function loginWhere(login: LoginRequired): string {
  return [login.host && `on ${login.host}`, login.user && `as ${login.user}`].filter(Boolean).join(" ");
}

// While the agent starts (lifecycle "starting") and finds no login,
// AgentAPI starts it again every few seconds, so it notices the login by
// itself. A login lost while the agent ran needs Check again, which
// restarts the agent on the same session.
export function loginNote(agentStarting: boolean): string {
  return agentStarting
    ? "AgentAPI notices the login by itself within a few seconds, or press Check again."
    : "Then press Check again: AgentAPI restarts the agent, which resumes this session.";
}

// What a Check again (POST /login/retry) did. "not-waiting" (409): the
// agent no longer waits for a login, so the status is just out of date.
export type LoginRetryOutcome = "checking" | "not-waiting";

// The notice the server also reports as an agent_error when the agent needs
// a login, for clients without the card: "<message> Run `<command>` in a
// terminal[ on <host> as <user>]; AgentAPI picks up the login by itself."
const LOGIN_NOTICE = /Run `[^`]+` in a terminal[^;]*; AgentAPI picks up the login by itself\.$/;

// Whether an agent_error is a login notice the chat doesn't show: while the
// agent waits for the login the card says the same, and the server replays
// its stored errors on every connection, also once the login is fixed.
export function isLoginNotice(message: string, login: LoginRequired | null): boolean {
  if (login && message.startsWith(login.message) && message.includes(`\`${login.command}\``)) return true;
  return LOGIN_NOTICE.test(message);
}
