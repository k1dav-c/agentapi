import type {TemporalConfig, TemporalConfigResponse, TemporalProfilesResponse} from "./temporal-config";
import {authFetch, withAuthRetry} from "./auth";
import type {
  FileUploadResponse,
  QueuedMessage,
  SendResult,
} from "@/components/chat-provider";
import {parseTransport, type DispatchMode, type Transport} from "./dispatch";
import type {NativeRequest, RequestResponse} from "./native-requests";
import {parseLoginRequired, type LoginRequired, type LoginRetryOutcome} from "./login";
import {ApiError} from "./error-utils";

export interface UploadOptions {
  signal?: AbortSignal;
  onProgress?: (progress: number) => void;
}

export interface MCPConfig {
  servers: Record<string, unknown>;
  path: string;
  restarted?: boolean;
}

export interface MCPCheckResult {
  name: string;
  status: "ready" | "unreachable" | "invalid";
  kind: "stdio" | "http" | "unknown";
  detail: string;
  latency_ms?: number;
}

export interface MCPProfiles {
  profiles: Record<string, Record<string, unknown>>;
  path: string;
}

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

const TEMPORAL_FAILED = "Temporal settings request failed";

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

    getTemporal(): Promise<TemporalConfigResponse> {
      return jsonRequest("/temporal", {fallback: TEMPORAL_FAILED});
    },

    updateTemporal(config: TemporalConfig): Promise<TemporalConfigResponse> {
      return jsonRequest("/temporal", {method: "PUT", body: {config}, fallback: TEMPORAL_FAILED});
    },

    getTemporalProfiles(): Promise<TemporalProfilesResponse> {
      return jsonRequest("/temporal/profiles", {fallback: TEMPORAL_FAILED});
    },

    saveTemporalProfile(name: string, config: TemporalConfig): Promise<TemporalProfilesResponse> {
      return jsonRequest(`/temporal/profiles/${encodeURIComponent(name)}`, {method: "PUT", body: {config}, fallback: TEMPORAL_FAILED});
    },

    importTemporalProfiles(profiles: Record<string, TemporalConfig>): Promise<TemporalProfilesResponse> {
      return jsonRequest("/temporal/profiles", {method: "PUT", body: {profiles}, fallback: TEMPORAL_FAILED});
    },

    deleteTemporalProfile(name: string): Promise<TemporalProfilesResponse> {
      return jsonRequest(`/temporal/profiles/${encodeURIComponent(name)}`, {method: "DELETE", fallback: TEMPORAL_FAILED});
    },

    applyTemporalProfile(name: string): Promise<TemporalConfigResponse> {
      return jsonRequest(`/temporal/profiles/${encodeURIComponent(name)}/apply`, {method: "POST", fallback: TEMPORAL_FAILED});
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
      const data = await jsonRequest<{requests?: NativeRequest[]}>("/requests", {fallback: "Failed to load the agent's requests"});
      return data.requests ?? [];
    },

    async respondRequest(id: string, body: RequestResponse): Promise<NativeRequest> {
      const data = await jsonRequest<NativeRequest & {$schema?: string}>(`/requests/${encodeURIComponent(id)}/respond`, {
        method: "POST",
        body,
        fallback: "Failed to answer the agent",
      });
      delete data.$schema;
      return data;
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

    async getTimelineEvents(): Promise<unknown[]> {
      const data = await jsonRequest<{events?: unknown[]} | undefined>("/timeline", {fallback: "Failed to export the current session"});
      if (!Array.isArray(data?.events)) {
        throw new Error("The server returned an invalid timeline response");
      }
      return data.events;
    },
  };
}
