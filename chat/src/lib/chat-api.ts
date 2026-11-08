import {authFetch, withAuthRetry} from "./auth";
import type {DispatchMode} from "./dispatch";
import type {QueuedMessage} from "./queue";
import {normalizeRequest, type NativeRequest, type RequestPayload, type RequestResponse} from "./native-requests";
import {normalizeRichMessage, type RichMessage} from "./conversation";
import {parseLoginRequired, type LoginRetryOutcome} from "./login";
import {ApiError} from "./error-utils";
import type {components, paths} from "./api-schema";

export interface UploadOptions {
  signal?: AbortSignal;
  onProgress?: (progress: number) => void;
}

// The API's types, generated from openapi.json (lib/api-schema.ts).
type Schemas = components["schemas"];
// A response body without huma's link to its JSON schema.
type Body<T> = Omit<T, "$schema">;

// POST /message: whether the server took the message, and what it did
// with it.
export type SendResult = Body<Schemas["MessageResponseBody"]>;
// POST /upload, or why the upload failed (error, without a filePath).
export type FileUploadResponse = Omit<Body<Schemas["UploadResponseBody"]>, "filePath"> & {filePath?: string; error?: string};

// GET and PUT /webhook: where the events go, which, and how they're signed.
export type WebhookConfig = Body<Schemas["WebhookConfigResponseBody"]>;
export type WebhookChange = Body<Schemas["WebhookConfigRequestBody"]>;
export type WebhookTest = Body<Schemas["WebhookTestResponseBody"]>;

// A file in the agent's working directory (GET /files/content).
export type FileContent = Body<Schemas["FileContentResponseBody"]>;
export type FileDiffStatus = Schemas["FileDiffStatus"];
// An HTML file as a page that shows by itself (GET /files/preview): what it
// refers to by relative path is inlined, for a sandboxed iframe's srcdoc.
export type FilePreview = Body<Schemas["FilePreviewResponseBody"]>;
// A file's uncommitted changes (GET /files/diff).
export type FileDiff = Body<Schemas["FileDiffResponseBody"]>;
// The files whose path ends with one that isn't there (GET /files/find).
export type FileMatches = Body<Schemas["FileFindResponseBody"]>;

export type PermissionMode = Schemas["PermissionMode"];
// The agent's session settings (native transport). A setting the agent
// doesn't report is missing; permission_mode is missing while the agent
// runs in a mode of its own (e.g. Claude Code's acceptEdits).
export type AgentSettings = Body<Schemas["Settings"]>;
export type ModelOption = Schemas["ModelOption"];
export type SettingOptions = Schemas["SettingOptions"];
// GET and PUT /settings.
export type SettingsResponse = Body<Schemas["SettingsResponseBody"]>;

// What the agent reports of its limits (native transport): how full its
// context window is, and the account's usage limits.
export type ContextUsage = Schemas["ContextUsage"];
export type RateLimit = Schemas["RateLimit"];
export type AgentUsage = Schemas["AgentUsage"];
export type PromptCache = Schemas["PromptCache"];
// A turn that stopped because the account reached a usage limit (native
// transport): the queue waits until the limit resets.
export type LimitStop = Schemas["LimitStop"];
export type ContinueAt = Schemas["ContinueAt"];
// A turn that failed on an upstream error that may pass (an HTTP 5xx, an
// overloaded API): AgentAPI tries it again at retry_at, and the queue
// waits until then.
export type RetryWait = Schemas["RetryWait"];
// One of the agent's sessions in the working directory (GET /sessions).
export type SessionSummary = Schemas["SessionInfo"];
export type SessionList = Body<Schemas["SessionsResponseBody"]>;
// What a session spent at the model providers' list prices (API-equivalent).
export type SessionCost = Schemas["SessionCost"];
export type HourCost = Schemas["HourCost"];
export type ModelCall = Schemas["ModelCall"];
export type Tokens = Schemas["Tokens"];
// What the open session spent, and its model calls (GET /cost).
export type CostReport = Body<Schemas["CostResponseBody"]>;
// A subagent the agent ran in its session, and what it did (GET
// /subagents, GET /subagents/{id}).
export type Subagent = Schemas["SubagentState"];

const SUBAGENT_STATUSES: Record<Subagent["status"], true> = {running: true, completed: true, failed: true, unknown: true};

// normalizeSubagent returns subagent with a status this chat knows: one a
// newer server added is unknown.
export function normalizeSubagent(subagent: Subagent): Subagent {
  return Object.hasOwn(SUBAGENT_STATUSES, subagent.status) ? subagent : {...subagent, status: "unknown"};
}
export type SubagentRun = Omit<Body<Schemas["SubagentResponseBody"]>, "messages"> & {messages: RichMessage[]};

interface APIErrorDetail {
  message: string;
}

interface APIErrorModel {
  detail: string;
  errors?: APIErrorDetail[];
}

// The server's error message (huma's detail plus field errors), or fallback.
async function errorDetail(response: Response, fallback: string): Promise<string> {
  const error = await response.json().catch(() => null) as APIErrorModel | null;
  const details = error?.errors?.map((item) => item.message).filter(Boolean).join(", ");
  if (!error?.detail) return details || fallback;
  return details ? `${error.detail}: ${details}` : error.detail;
}

// What the chat reads of GET /status: the login the agent waits for, if
// any.
type StatusBody = Body<Schemas["StatusResponseBody"]>;
export type ServerStatusInfo = {login_required: NonNullable<StatusBody["login_required"]> | null};

// value if it is an http(s) URL. The chat opens such links, so a
// javascript: URL must not get through.
export function httpURL(value: unknown, base?: string): string | undefined {
  if (typeof value !== "string" || !value) return undefined;
  try {
    const url = new URL(value, base);
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : undefined;
  } catch {
    return undefined;
  }
}

type Method = "GET" | "PUT" | "POST" | "DELETE";
// The API's routes that take method, as openapi.json has them
// ("/queue/{id}"): a route the server doesn't have fails to typecheck.
type Route<M extends Method> = {[P in keyof paths]: paths[P][Lowercase<M>] extends undefined ? never : P}[keyof paths];
// The {name}s in a route.
type Params<P extends string> = P extends `${string}{${infer Name}}${infer Rest}` ? Name | Params<Rest> : never;
type Query = Record<string, string | number | boolean | undefined>;
type RouteArgs<P extends string> = [Params<P>] extends [never]
  ? [params?: undefined, query?: Query]
  : [params: Record<Params<P>, string | number>, query?: Query];

// A request's method and its path below the API's base URL.
interface APIRoute {
  method: Method;
  path: string;
}

// route returns the request of method to path, with its {name}s filled in
// from params and query after it (its undefined values left out), each
// URI-encoded.
export function route<M extends Method, P extends Route<M>>(method: M, path: P, ...[params, query]: RouteArgs<P>): APIRoute {
  const filled = (path as string).replace(/\{(\w+)\}/g, (_, name: string) => encodeURIComponent(String((params as Record<string, string | number>)[name])));
  const pairs = Object.entries(query ?? {}).flatMap(([key, value]) =>
    value === undefined ? [] : [`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`],
  );
  return {method, path: pairs.length > 0 ? `${filled}?${pairs.join("&")}` : filled};
}

interface RequestOptions {
  // Sent as JSON.
  body?: unknown;
  // The error message when the server doesn't give one.
  fallback: string;
}

export function createChatAPI(baseURL: string) {
  // A request to the API: the JSON it answers with, or an ApiError with the
  // server's message and the status. A 401 that is left after authFetch
  // asked for the token means the user didn't sign in.
  const jsonRequest = async <T = unknown>({method, path}: APIRoute, {body, fallback}: RequestOptions): Promise<T> => {
    const response = await authFetch(baseURL, `${baseURL}${path}`, {
      method,
      headers: body === undefined ? undefined : {"Content-Type": "application/json"},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      const message = response.status === 401
        ? "Not signed in: this server needs its API token."
        : await errorDetail(response, fallback);
      throw new ApiError(message, response.status);
    }
    const text = await response.text();
    return (text ? JSON.parse(text) : undefined) as T;
  };

  return {
    async deleteMessages(): Promise<void> {
      await jsonRequest(route("DELETE", "/messages"), {fallback: "Failed to delete messages and restart the agent"});
    },

    getSettings(): Promise<SettingsResponse> {
      return jsonRequest(route("GET", "/settings"), {fallback: "Failed to load the agent's settings"});
    },

    // Changes the settings set in change and leaves the others.
    updateSettings(change: AgentSettings): Promise<SettingsResponse> {
      return jsonRequest(route("PUT", "/settings"), {body: change, fallback: "Failed to change the agent's settings"});
    },

    async compact(): Promise<void> {
      await jsonRequest(route("POST", "/compact"), {fallback: "Failed to compact the context"});
    },

    // Continues the turn a usage limit stopped: now, or once it resets.
    async continueAfterLimit(at: ContinueAt): Promise<void> {
      await jsonRequest(route("POST", "/limit/continue"), {body: {at}, fallback: "Failed to continue the task"});
    },

    async cancelContinue(): Promise<void> {
      await jsonRequest(route("DELETE", "/limit/continue"), {fallback: "Failed to cancel the continuation"});
    },

    // Forgets the limit stop, so the queued tasks go out.
    async dismissLimitStop(): Promise<void> {
      await jsonRequest(route("DELETE", "/limit"), {fallback: "Failed to dismiss the usage limit"});
    },

    // Tries the failed turn again now rather than at its retry_at.
    async retryNow(): Promise<void> {
      await jsonRequest(route("POST", "/retry"), {fallback: "Failed to retry the task"});
    },

    // Forgets the failed turn without trying it again, so the queued tasks
    // go out.
    async cancelRetry(): Promise<void> {
      await jsonRequest(route("DELETE", "/retry"), {fallback: "Failed to cancel the retry"});
    },

    async listSubagents(): Promise<Subagent[]> {
      const list = await jsonRequest<{subagents?: Subagent[]}>(route("GET", "/subagents"), {fallback: "Failed to list the subagents"});
      return (list.subagents ?? []).map(normalizeSubagent);
    },

    async getSubagent(id: string): Promise<SubagentRun> {
      const run = await jsonRequest<Body<Schemas["SubagentResponseBody"]>>(route("GET", "/subagents/{id}", {id}), {fallback: "Failed to read the subagent"});
      return {...run, subagent: normalizeSubagent(run.subagent), messages: (run.messages ?? []).map(normalizeRichMessage)};
    },

    async listSessions(): Promise<SessionList> {
      const list = await jsonRequest<SessionList>(route("GET", "/sessions"), {fallback: "Failed to list the agent's sessions"});
      return {sessions: list.sessions ?? [], current: list.current, total_cost: list.total_cost};
    },

    // What the open session spent. Given the report read before, it asks
    // for the calls made since only, and puts them after the known ones;
    // the server sends them all when the known ones changed.
    async getCost(known?: CostReport | null): Promise<CostReport> {
      const since = known && known.total_calls > 0 && known.digest ? {from: known.total_calls, digest: known.digest} : undefined;
      const report = await jsonRequest<CostReport>(route("GET", "/cost", undefined, since), {fallback: "Failed to read what the session spent"});
      return mergeCost(known, report);
    },

    // Opens one of the agent's sessions, or a copy of it, in place of this
    // conversation.
    async resumeSession(id: string, fork: boolean): Promise<void> {
      await jsonRequest(route("POST", "/sessions/{id}/resume", {id}), {
        body: fork ? {fork} : {},
        fallback: "Failed to open the session",
      });
    },

    // Types keys into the native transport's terminal.
    async terminalInput(data: string): Promise<void> {
      await jsonRequest(route("POST", "/terminal/input"), {body: {data}, fallback: "Failed to type into the terminal"});
    },

    async terminalResize(cols: number, rows: number): Promise<void> {
      await jsonRequest(route("POST", "/terminal/resize"), {body: {cols, rows}, fallback: "Failed to resize the terminal"});
    },

    getWebhook(): Promise<WebhookConfig> {
      return jsonRequest(route("GET", "/webhook"), {fallback: "Failed to read the webhook settings"});
    },

    updateWebhook(change: WebhookChange): Promise<WebhookConfig> {
      return jsonRequest(route("PUT", "/webhook"), {body: change, fallback: "Failed to save the webhook settings"});
    },

    // Records that the user saw the conversation now, so the tasks that
    // ended are read for every client (POST /tasks/seen).
    async markSeen(): Promise<void> {
      await jsonRequest(route("POST", "/tasks/seen"), {fallback: "Failed to mark the tasks seen"});
    },

    // Sends a webhook.test event now and says how the receiver answered.
    testWebhook(): Promise<WebhookTest> {
      return jsonRequest(route("POST", "/webhook/test"), {fallback: "Failed to send the test event"});
    },

    // An empty queue when the server can't say.
    async getQueue(): Promise<QueuedMessage[]> {
      const response = await authFetch(baseURL, `${baseURL}${route("GET", "/queue").path}`);
      if (!response.ok) return [];
      const data = (await response.json()) as {messages?: QueuedMessage[]};
      return data.messages ?? [];
    },

    async getStatus(): Promise<ServerStatusInfo> {
      const data = await jsonRequest<Partial<StatusBody>>(route("GET", "/status"), {
        fallback: "Failed to read the agent status",
      });
      return {
        login_required: parseLoginRequired(data.login_required),
      };
    },

    // The Coder workspace the agent runs in, if any.
    async getWorkspaceUrl(): Promise<string | undefined> {
      const data = await jsonRequest<{url?: string} | undefined>(route("GET", "/workspace"), {fallback: "Failed to read the workspace"});
      return httpURL(data?.url);
    },

    // Asks AgentAPI to check the agent's login again now.
    async retryLogin(): Promise<LoginRetryOutcome> {
      try {
        await jsonRequest(route("POST", "/login/retry"), {fallback: "Failed to check the login"});
        return "checking";
      } catch (error) {
        if (error instanceof ApiError && error.status === 409) return "not-waiting";
        throw error;
      }
    },

    async sendMessage(content: string, mode: DispatchMode = "queue"): Promise<SendResult> {
      const result = await jsonRequest<Partial<SendResult>>(route("POST", "/message"), {
        // Only send dispatch_mode when it isn't the default, so older
        // servers that don't know the field keep accepting messages.
        body: mode === "queue" ? {content, type: "user"} : {content, type: "user", dispatch_mode: mode},
        fallback: "Failed to send the message",
      });
      return {ok: result.ok === true, queued: result.queued === true, steered: result.steered === true};
    },

    async interrupt(): Promise<void> {
      await jsonRequest(route("POST", "/interrupt"), {fallback: "Failed to stop the agent"});
    },

    async getRequests(): Promise<NativeRequest[]> {
      const data = await jsonRequest<{requests?: RequestPayload[]}>(route("GET", "/requests"), {fallback: "Failed to load the agent's requests"});
      return (data.requests ?? []).map(normalizeRequest);
    },

    async respondRequest(id: string, body: RequestResponse): Promise<NativeRequest> {
      const data = await jsonRequest<RequestPayload & {$schema?: string}>(route("POST", "/requests/{id}/respond", {id}), {
        body,
        fallback: "Failed to answer the agent",
      });
      delete data.$schema;
      return normalizeRequest(data);
    },

    async steerQueuedMessage(id: number): Promise<void> {
      await jsonRequest(route("POST", "/queue/{id}/steer", {id}), {fallback: "Failed to add the task to the current turn"});
    },

    // 409: the queue changed in the meantime.
    async reorderQueue(ids: number[]): Promise<void> {
      await jsonRequest(route("PUT", "/queue"), {body: {ids}, fallback: "Failed to reorder the queue"});
    },

    uploadFiles(
      formData: FormData,
      options: UploadOptions = {},
    ): Promise<FileUploadResponse> {
      const send = (credentials: RequestCredentials) => new Promise<{status: number; result: FileUploadResponse}>((resolve) => {
        const done = (status: number, result: FileUploadResponse) => resolve({status, result});
        if (options.signal?.aborted) {
          done(0, {ok: false, error: "Upload cancelled."});
          return;
        }
        const request = new XMLHttpRequest();
        const upload = route("POST", "/upload");
        request.open(upload.method, `${baseURL}${upload.path}`);
        // Same-origin requests send the cookie either way.
        request.withCredentials = credentials === "include";
        request.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            options.onProgress?.(
              Math.min(100, Math.round((event.loaded / event.total) * 100)),
            );
          }
        };
        request.onload = () => {
          if (request.status === 401) {
            // withAuthRetry asks for the token and uploads again; this is
            // the answer when nobody signs in.
            done(401, {ok: false, error: "Sign in to upload files."});
            return;
          }
          try {
            const data = JSON.parse(request.responseText) as
              | FileUploadResponse
              | APIErrorModel;
            if (request.status >= 200 && request.status < 300) {
              options.onProgress?.(100);
              done(request.status, data as FileUploadResponse);
              return;
            }
            done(request.status, {
              ok: false,
              error: "detail" in data ? data.detail : "The upload was rejected.",
            });
          } catch {
            done(request.status, {
              ok: false,
              error: "The server returned an invalid response.",
            });
          }
        };
        request.onerror = () =>
          done(0, {ok: false, error: "The upload connection failed."});
        request.onabort = () =>
          done(0, {ok: false, error: "Upload cancelled."});
        options.signal?.addEventListener("abort", () => request.abort(), {
          once: true,
        });
        request.send(formData);
      });
      return withAuthRetry(baseURL, send).then(({result}) => result);
    },

    async updateQueuedMessage(id: number, content: string) {
      await jsonRequest(route("PUT", "/queue/{id}", {id}), {body: {content}, fallback: "Failed to update queued message"});
    },

    async deleteQueuedMessage(id: number) {
      await jsonRequest(route("DELETE", "/queue/{id}", {id}), {fallback: "Failed to delete queued message"});
    },

    // A file in the agent's working directory, by its path as the chat shows
    // it (absolute, or relative to that directory; without a :line suffix).
    // ApiError 403: outside that directory, 404: no such file, 400: a
    // directory.
    async getFileContent(path: string): Promise<FileContent> {
      const data = await jsonRequest<Partial<FileContent>>(route("GET", "/files/content", undefined, {path}), {
        fallback: "Failed to read the file",
      });
      return {
        path: data.path ?? path,
        relative: data.relative || path,
        size: data.size ?? 0,
        content: data.content ?? "",
        truncated: data.truncated === true,
        binary: data.binary === true,
      };
    },

    // The uncommitted changes of a file in the agent's working directory.
    // ApiError 403: outside that directory.
    async getFileDiff(path: string): Promise<FileDiff> {
      const data = await jsonRequest<Partial<FileDiff>>(route("GET", "/files/diff", undefined, {path}), {
        fallback: "Failed to read the file's changes",
      });
      return {
        path: data.path ?? path,
        relative: data.relative || path,
        status: data.status ?? "unchanged",
        diff: data.diff ?? "",
        truncated: data.truncated === true,
      };
    },

    // An HTML file in the agent's working directory with what it refers to
    // inlined, for the file viewer's preview. ApiError 400: not an HTML
    // file, or too large.
    async findFile(path: string): Promise<FileMatches> {
      const data = await jsonRequest<Partial<FileMatches>>(route("GET", "/files/find", undefined, {path}), {fallback: "Failed to search for the file"});
      return {matches: data.matches ?? [], complete: data.complete ?? true};
    },

    async getFilePreview(path: string): Promise<FilePreview> {
      const data = await jsonRequest<Partial<FilePreview>>(route("GET", "/files/preview", undefined, {path}), {
        fallback: "Failed to preview the file",
      });
      return {
        path: data.path ?? path,
        relative: data.relative || path,
        html: data.html ?? "",
        missing: Array.isArray(data.missing) ? data.missing : [],
      };
    },

    // The URL of an image file in the agent's working directory (GET
    // /files/image), which the session cookie authenticates. version makes
    // a new URL, to show the file again after it changed.
    // The route is the router's, not in openapi.json.
    fileImageURL(path: string, version = 0): string {
      return `${baseURL}/files/image?path=${encodeURIComponent(path)}${version ? `&v=${version}` : ""}`;
    },

    async getTimelineEvents(): Promise<unknown[]> {
      // With the subagents' events, after the agent's own.
      const data = await jsonRequest<{events?: unknown[]} | undefined>(route("GET", "/timeline", undefined, {subagents: true}), {fallback: "Failed to export the current session"});
      if (!Array.isArray(data?.events)) {
        throw new Error("The server returned an invalid timeline response");
      }
      return data.events;
    },
  };
}

// The API calls of one server (createChatAPI).
export type ChatAPI = ReturnType<typeof createChatAPI>;

// mergeCost puts a cost report's calls after the known ones when it holds
// the calls after them (from > 0), and stands alone otherwise.
export function mergeCost(known: CostReport | null | undefined, report: CostReport): CostReport {
  const calls = report.calls ?? [];
  if (!known || report.from <= 0 || report.from !== known.total_calls || known.agent_session_id !== report.agent_session_id) {
    return {...report, calls, from: 0};
  }
  return {...report, calls: [...(known.calls ?? []), ...calls], from: 0};
}
