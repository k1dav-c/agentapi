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

// While the agent waits at startup AgentAPI starts it again every few
// seconds, so it notices the login by itself. A login lost while the agent
// ran needs Check again, which restarts the agent on the same session.
export function loginNote(waitingAtStart: boolean): string {
  return waitingAtStart
    ? "AgentAPI notices the login by itself within a few seconds, or press Check again."
    : "Then press Check again: AgentAPI restarts the agent, which resumes this session.";
}

// What a Check again (POST /login/retry) did. 409: the agent no longer
// waits for a login, so the status is just out of date.
export type LoginRetryOutcome = "checking" | "not-waiting";

export function loginRetryOutcome(status: number): LoginRetryOutcome | null {
  if (status >= 200 && status < 300) return "checking";
  if (status === 409) return "not-waiting";
  return null;
}

// Whether an agent_error only repeats the login the card shows (the server
// also reports it as an error, for clients without the card).
export function isLoginNotice(message: string, login: LoginRequired | null): boolean {
  return login !== null && message.startsWith(login.message) && message.includes(`\`${login.command}\``);
}
