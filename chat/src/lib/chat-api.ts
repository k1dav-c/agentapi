import {authFetch, withAuthRetry} from "./auth";
import type {FileUploadResponse, QueuedMessage, SendResult} from "@/components/chat-provider";
import {parseTransport, type DispatchMode, type Transport} from "./dispatch";
import {normalizeRequest, type NativeRequest, type RequestPayload, type RequestResponse} from "./native-requests";
import {normalizeRichMessage, type RichMessage} from "./conversation";
import {parseLoginRequired, type LoginRequired, type LoginRetryOutcome} from "./login";
import {ApiError} from "./error-utils";
import type {components} from "./api-schema";

export interface UploadOptions {
  signal?: AbortSignal;
  onProgress?: (progress: number) => void;
}

// The API's types, generated from openapi.json (lib/api-schema.ts).
type Schemas = components["schemas"];
// A response body without huma's link to its JSON schema.
type Body<T> = Omit<T, "$schema">;

// GET and PUT /mcp. restarted is set by a change that restarted the agent.
export type MCPConfig = Body<Schemas["MCPGetResponseBody"]> & {restarted?: boolean};
export type MCPCheckResult = Schemas["MCPCheckResult"];
export type MCPProfiles = Body<Schemas["MCPProfilesResponseBody"]>;
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

export interface ServerStatusInfo {
  transport: Transport;
  // Set while the agent waits for someone to log it in (native transport).
  login_required: LoginRequired | null;
}

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

interface RequestOptions {
  method?: string;
  // Sent as JSON.
  body?: unknown;
  // The error message when the server doesn't give one.
  fallback: string;
}

export function createChatAPI(baseURL: string) {
  // A request to the API: the JSON it answers with, or an ApiError with the
  // server's message and the status. A 401 that is left after authFetch
  // asked for the token means the user didn't sign in.
  const jsonRequest = async <T = unknown>(path: string, {method = "GET", body, fallback}: RequestOptions): Promise<T> => {
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
      await jsonRequest("/messages", {method: "DELETE", fallback: "Failed to delete messages and restart the agent"});
    },

    getSettings(): Promise<SettingsResponse> {
      return jsonRequest("/settings", {fallback: "Failed to load the agent's settings"});
    },

    // Changes the settings set in change and leaves the others.
    updateSettings(change: AgentSettings): Promise<SettingsResponse> {
      return jsonRequest("/settings", {method: "PUT", body: change, fallback: "Failed to change the agent's settings"});
    },

    async compact(): Promise<void> {
      await jsonRequest("/compact", {method: "POST", fallback: "Failed to compact the context"});
    },

    // Continues the turn a usage limit stopped: now, or once it resets.
    async continueAfterLimit(at: ContinueAt): Promise<void> {
      await jsonRequest("/limit/continue", {method: "POST", body: {at}, fallback: "Failed to continue the task"});
    },

    async cancelContinue(): Promise<void> {
      await jsonRequest("/limit/continue", {method: "DELETE", fallback: "Failed to cancel the continuation"});
    },

    // Forgets the limit stop, so the queued tasks go out.
    async dismissLimitStop(): Promise<void> {
      await jsonRequest("/limit", {method: "DELETE", fallback: "Failed to dismiss the usage limit"});
    },

    async listSubagents(): Promise<Subagent[]> {
      const list = await jsonRequest<{subagents?: Subagent[]}>("/subagents", {fallback: "Failed to list the subagents"});
      return list.subagents ?? [];
    },

    async getSubagent(id: string): Promise<SubagentRun> {
      const run = await jsonRequest<Body<Schemas["SubagentResponseBody"]>>(`/subagents/${encodeURIComponent(id)}`, {fallback: "Failed to read the subagent"});
      return {...run, messages: (run.messages ?? []).map(normalizeRichMessage)};
    },

    async listSessions(): Promise<SessionList> {
      const list = await jsonRequest<SessionList>("/sessions", {fallback: "Failed to list the agent's sessions"});
      return {sessions: list.sessions ?? [], current: list.current, total_cost: list.total_cost};
    },

    async getCost(): Promise<CostReport> {
      const report = await jsonRequest<CostReport>("/cost", {fallback: "Failed to read what the session spent"});
      return {...report, calls: report.calls ?? []};
    },

    // Opens one of the agent's sessions, or a copy of it, in place of this
    // conversation.
    async resumeSession(id: string, fork: boolean): Promise<void> {
      await jsonRequest(`/sessions/${encodeURIComponent(id)}/resume`, {
        method: "POST",
        body: fork ? {fork} : {},
        fallback: "Failed to open the session",
      });
    },

    // Types keys into the native transport's terminal.
    async terminalInput(data: string): Promise<void> {
      await jsonRequest("/terminal/input", {method: "POST", body: {data}, fallback: "Failed to type into the terminal"});
    },

    async terminalResize(cols: number, rows: number): Promise<void> {
      await jsonRequest("/terminal/resize", {method: "POST", body: {cols, rows}, fallback: "Failed to resize the terminal"});
    },

    getWebhook(): Promise<WebhookConfig> {
      return jsonRequest("/webhook", {fallback: "Failed to read the webhook settings"});
    },

    updateWebhook(change: WebhookChange): Promise<WebhookConfig> {
      return jsonRequest("/webhook", {method: "PUT", body: change, fallback: "Failed to save the webhook settings"});
    },

    // Records that the user saw the conversation now, so the tasks that
    // ended are read for every client (POST /tasks/seen).
    async markSeen(): Promise<void> {
      await jsonRequest("/tasks/seen", {method: "POST", fallback: "Failed to mark the tasks seen"});
    },

    // Sends a webhook.test event now and says how the receiver answered.
    testWebhook(): Promise<WebhookTest> {
      return jsonRequest("/webhook/test", {method: "POST", fallback: "Failed to send the test event"});
    },

    getMCP(): Promise<MCPConfig> {
      return jsonRequest("/mcp", {fallback: "MCP config management is unavailable"});
    },

    async updateMCP(
      servers: Record<string, unknown>,
      restart = false,
    ): Promise<MCPConfig> {
      const result = await jsonRequest<{path: string; restarted?: boolean}>(`/mcp${restart ? "?restart=true" : ""}`, {
        method: "PUT",
        body: {servers},
        fallback: "Failed to update MCP servers",
      });
      return {servers, path: result.path, restarted: result.restarted};
    },

    async checkMCP(servers?: Record<string, unknown>): Promise<MCPCheckResult[]> {
      const result = await jsonRequest<{results: MCPCheckResult[]}>("/mcp/check", {
        method: "POST",
        body: servers ? {servers} : {},
        fallback: "Failed to check MCP servers",
      });
      return result.results;
    },

    async createMCPServer(name: string, config: unknown, restart = false) {
      await jsonRequest(`/mcp/servers${restart ? "?restart=true" : ""}`, {
        method: "POST",
        body: {name, config},
        fallback: "Failed to create MCP server",
      });
    },

    async updateMCPServer(name: string, config: unknown, restart = false) {
      await jsonRequest(`/mcp/servers/${encodeURIComponent(name)}${restart ? "?restart=true" : ""}`, {
        method: "PATCH",
        body: {config},
        fallback: "Failed to update MCP server",
      });
    },

    async deleteMCPServer(name: string, restart = false) {
      await jsonRequest(`/mcp/servers/${encodeURIComponent(name)}${restart ? "?restart=true" : ""}`, {
        method: "DELETE",
        fallback: "Failed to delete MCP server",
      });
    },

    getMCPProfiles(): Promise<MCPProfiles> {
      return jsonRequest("/mcp/profiles", {fallback: "Failed to load MCP profiles"});
    },

    saveMCPProfile(name: string, servers: Record<string, unknown>): Promise<MCPProfiles> {
      return jsonRequest(`/mcp/profiles/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: {servers},
        fallback: "Failed to save MCP profile",
      });
    },

    deleteMCPProfile(name: string): Promise<MCPProfiles> {
      return jsonRequest(`/mcp/profiles/${encodeURIComponent(name)}`, {method: "DELETE", fallback: "Failed to delete MCP profile"});
    },

    async applyMCPProfile(name: string, restart = false) {
      await jsonRequest(`/mcp/profiles/${encodeURIComponent(name)}/apply${restart ? "?restart=true" : ""}`, {
        method: "POST",
        fallback: "Failed to apply MCP profile",
      });
    },

    // An empty queue when the server can't say.
    async getQueue(): Promise<QueuedMessage[]> {
      const response = await authFetch(baseURL, `${baseURL}/queue`);
      if (!response.ok) return [];
      const data = (await response.json()) as {messages?: QueuedMessage[]};
      return data.messages ?? [];
    },

    async getStatus(): Promise<ServerStatusInfo> {
      const data = await jsonRequest<{transport?: unknown; login_required?: unknown}>("/status", {
        fallback: "Failed to read the agent status",
      });
      return {
        transport: parseTransport(data.transport) ?? "pty",
        login_required: parseLoginRequired(data.login_required),
      };
    },

    // The Coder workspace the agent runs in, if any.
    async getWorkspaceUrl(): Promise<string | undefined> {
      const data = await jsonRequest<{url?: string} | undefined>("/workspace", {fallback: "Failed to read the workspace"});
      return httpURL(data?.url);
    },

    // Asks AgentAPI to check the agent's login again now.
    async retryLogin(): Promise<LoginRetryOutcome> {
      try {
        await jsonRequest("/login/retry", {method: "POST", fallback: "Failed to check the login"});
        return "checking";
      } catch (error) {
        if (error instanceof ApiError && error.status === 409) return "not-waiting";
        throw error;
      }
    },

    async sendMessage(
      content: string,
      type: "user" | "raw",
      mode: DispatchMode = "queue",
    ): Promise<SendResult> {
      const result = await jsonRequest<{ok?: boolean; queued?: boolean; steered?: boolean}>("/message", {
        method: "POST",
        // Only send dispatch_mode when it isn't the default, so older
        // servers that don't know the field keep accepting messages.
        body: mode === "queue" ? {content, type} : {content, type, dispatch_mode: mode},
        fallback: "Failed to send the message",
      });
      return {ok: result.ok === true, queued: result.queued === true, steered: result.steered === true};
    },

    async interrupt(): Promise<void> {
      await jsonRequest("/interrupt", {method: "POST", fallback: "Failed to stop the agent"});
    },

    async getRequests(): Promise<NativeRequest[]> {
      const data = await jsonRequest<{requests?: RequestPayload[]}>("/requests", {fallback: "Failed to load the agent's requests"});
      return (data.requests ?? []).map(normalizeRequest);
    },

    async respondRequest(id: string, body: RequestResponse): Promise<NativeRequest> {
      const data = await jsonRequest<RequestPayload & {$schema?: string}>(`/requests/${encodeURIComponent(id)}/respond`, {
        method: "POST",
        body,
        fallback: "Failed to answer the agent",
      });
      delete data.$schema;
      return normalizeRequest(data);
    },

    async steerQueuedMessage(id: number): Promise<void> {
      await jsonRequest(`/queue/${id}/steer`, {method: "POST", fallback: "Failed to add the task to the current turn"});
    },

    // 409: the queue changed in the meantime.
    async reorderQueue(ids: number[]): Promise<void> {
      await jsonRequest("/queue", {method: "PUT", body: {ids}, fallback: "Failed to reorder the queue"});
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
        request.open("POST", `${baseURL}/upload`);
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
      await jsonRequest(`/queue/${id}`, {method: "PUT", body: {content}, fallback: "Failed to update queued message"});
    },

    async deleteQueuedMessage(id: number) {
      await jsonRequest(`/queue/${id}`, {method: "DELETE", fallback: "Failed to delete queued message"});
    },

    // A file in the agent's working directory, by its path as the chat shows
    // it (absolute, or relative to that directory; without a :line suffix).
    // ApiError 403: outside that directory, 404: no such file, 400: a
    // directory.
    async getFileContent(path: string): Promise<FileContent> {
      const data = await jsonRequest<Partial<FileContent>>(`/files/content?path=${encodeURIComponent(path)}`, {
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
      const data = await jsonRequest<Partial<FileDiff>>(`/files/diff?path=${encodeURIComponent(path)}`, {
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
      const data = await jsonRequest<Partial<FileMatches>>(`/files/find?path=${encodeURIComponent(path)}`, {fallback: "Failed to search for the file"});
      return {matches: data.matches ?? [], complete: data.complete ?? true};
    },

    async getFilePreview(path: string): Promise<FilePreview> {
      const data = await jsonRequest<Partial<FilePreview>>(`/files/preview?path=${encodeURIComponent(path)}`, {
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
    fileImageURL(path: string, version = 0): string {
      return `${baseURL}/files/image?path=${encodeURIComponent(path)}${version ? `&v=${version}` : ""}`;
    },

    async getTimelineEvents(): Promise<unknown[]> {
      // With the subagents' events, after the agent's own.
      const data = await jsonRequest<{events?: unknown[]} | undefined>("/timeline?subagents=true", {fallback: "Failed to export the current session"});
      if (!Array.isArray(data?.events)) {
        throw new Error("The server returned an invalid timeline response");
      }
      return data.events;
    },
  };
}
