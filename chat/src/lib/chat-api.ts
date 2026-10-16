import type {TemporalConfig, TemporalConfigResponse, TemporalProfilesResponse} from "./temporal-config";
import {authFetch, markAuthRequired, requestSignIn, withCredentials} from "./auth";
import type {
  FileUploadResponse,
  QueuedMessage,
  SendResult,
} from "@/components/chat-provider";
import type {Capabilities, DispatchMode, Transport} from "./dispatch";
import {RespondError, type NativeRequest, type RequestResponse} from "./native-requests";

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

async function requireOK(response: Response, fallback: string) {
  if (!response.ok) throw new Error(fallback);
  return response;
}

// The server's error message (huma's detail plus field errors), or fallback.
async function errorDetail(response: Response, fallback: string): Promise<string> {
  const error = await response.json().catch(() => null) as APIErrorModel | null;
  const details = error?.errors?.map((item) => item.message).join(", ");
  if (!error?.detail) return fallback;
  return details ? `${error.detail}: ${details}` : error.detail;
}

export interface ServerStatusInfo {
  transport: Transport;
  capabilities?: Capabilities;
  pending_requests?: number;
}

async function temporalRequest<T>(baseURL: string, url: string, method = "GET", body?: unknown): Promise<T> {
  const response = await authFetch(baseURL, url, {
    method,
    headers: body === undefined ? undefined : {"Content-Type": "application/json"},
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    const error = await response.json().catch(() => null) as APIErrorModel | null;
    throw new Error(error?.detail || error?.errors?.[0]?.message || "Temporal settings request failed");
  }
  return response.json() as Promise<T>;
}

export function createChatAPI(baseURL: string) {
  const fetch = (url: string, init?: RequestInit) => authFetch(baseURL, url, init);
  return {
    async deleteMessages(): Promise<void> {
      await requireOK(
        await fetch(`${baseURL}/messages`, {method: "DELETE"}),
        "Failed to delete messages and restart the agent",
      );
    },

    async getTemporal(): Promise<TemporalConfigResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal`);
    },

    async updateTemporal(config: TemporalConfig): Promise<TemporalConfigResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal`, "PUT", {config});
    },

    async getTemporalProfiles(): Promise<TemporalProfilesResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal/profiles`);
    },

    async saveTemporalProfile(name: string, config: TemporalConfig): Promise<TemporalProfilesResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal/profiles/${encodeURIComponent(name)}`, "PUT", {config});
    },

    async importTemporalProfiles(profiles: Record<string, TemporalConfig>): Promise<TemporalProfilesResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal/profiles`, "PUT", {profiles});
    },

    async deleteTemporalProfile(name: string): Promise<TemporalProfilesResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal/profiles/${encodeURIComponent(name)}`, "DELETE");
    },

    async applyTemporalProfile(name: string): Promise<TemporalConfigResponse> {
      return temporalRequest(baseURL, `${baseURL}/temporal/profiles/${encodeURIComponent(name)}/apply`, "POST");
    },

    async getMCP(): Promise<MCPConfig> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp`),
        "MCP config management is unavailable",
      );
      return (await response.json()) as MCPConfig;
    },

    async updateMCP(
      servers: Record<string, unknown>,
      restart = false,
    ): Promise<MCPConfig> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp${restart ? "?restart=true" : ""}`, {
          method: "PUT",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({servers}),
        }),
        "Failed to update MCP servers",
      );
      const result = (await response.json()) as {
        path: string;
        restarted?: boolean;
      };
      return {servers, path: result.path, restarted: result.restarted};
    },

    async checkMCP(servers?: Record<string, unknown>): Promise<MCPCheckResult[]> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp/check`, {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify(servers ? {servers} : {}),
        }),
        "Failed to check MCP servers",
      );
      return ((await response.json()) as {results: MCPCheckResult[]}).results;
    },

    async createMCPServer(name: string, config: unknown, restart = false) {
      await requireOK(
        await fetch(`${baseURL}/mcp/servers${restart ? "?restart=true" : ""}`, {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({name, config}),
        }),
        "Failed to create MCP server",
      );
    },

    async updateMCPServer(name: string, config: unknown, restart = false) {
      await requireOK(
        await fetch(`${baseURL}/mcp/servers/${encodeURIComponent(name)}${restart ? "?restart=true" : ""}`, {
          method: "PATCH",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({config}),
        }),
        "Failed to update MCP server",
      );
    },

    async deleteMCPServer(name: string, restart = false) {
      await requireOK(
        await fetch(`${baseURL}/mcp/servers/${encodeURIComponent(name)}${restart ? "?restart=true" : ""}`, {
          method: "DELETE",
        }),
        "Failed to delete MCP server",
      );
    },

    async getMCPProfiles(): Promise<MCPProfiles> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp/profiles`),
        "Failed to load MCP profiles",
      );
      return (await response.json()) as MCPProfiles;
    },

    async saveMCPProfile(name: string, servers: Record<string, unknown>): Promise<MCPProfiles> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp/profiles/${encodeURIComponent(name)}`, {
          method: "PUT",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({servers}),
        }),
        "Failed to save MCP profile",
      );
      return (await response.json()) as MCPProfiles;
    },

    async deleteMCPProfile(name: string): Promise<MCPProfiles> {
      const response = await requireOK(
        await fetch(`${baseURL}/mcp/profiles/${encodeURIComponent(name)}`, {method: "DELETE"}),
        "Failed to delete MCP profile",
      );
      return (await response.json()) as MCPProfiles;
    },

    async applyMCPProfile(name: string, restart = false) {
      await requireOK(
        await fetch(`${baseURL}/mcp/profiles/${encodeURIComponent(name)}/apply${restart ? "?restart=true" : ""}`, {
          method: "POST",
        }),
        "Failed to apply MCP profile",
      );
    },

    async getQueue(): Promise<QueuedMessage[]> {
      const response = await fetch(`${baseURL}/queue`);
      if (!response.ok) return [];
      const data = (await response.json()) as {messages?: QueuedMessage[]};
      return data.messages ?? [];
    },

    async getStatus(): Promise<ServerStatusInfo> {
      const response = await requireOK(await fetch(`${baseURL}/status`), "Failed to read the agent status");
      const data = (await response.json()) as Partial<ServerStatusInfo>;
      return {
        transport: data.transport === "acp" || data.transport === "native" ? data.transport : "pty",
        capabilities: data.capabilities,
        pending_requests: data.pending_requests,
      };
    },

    async sendMessage(
      content: string,
      type: "user" | "raw",
      mode: DispatchMode = "queue",
    ): Promise<SendResult> {
      const response = await fetch(`${baseURL}/message`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        // Only send dispatch_mode when it isn't the default, so older
        // servers that don't know the field keep accepting messages.
        body: JSON.stringify(mode === "queue" ? {content, type} : {content, type, dispatch_mode: mode}),
      });
      if (response.status === 401) {
        throw new Error("Not signed in: this server needs its API token.");
      }
      if (!response.ok) {
        throw new Error(await errorDetail(response, "Failed to send the message"));
      }
      const result = (await response.json()) as {
        ok?: boolean;
        queued?: boolean;
        steered?: boolean;
      };
      return {ok: result.ok === true, queued: result.queued === true, steered: result.steered === true};
    },

    async interrupt(): Promise<void> {
      const response = await fetch(`${baseURL}/interrupt`, {method: "POST"});
      if (!response.ok) throw new Error(await errorDetail(response, "Failed to stop the agent"));
    },

    async getRequests(): Promise<NativeRequest[]> {
      const response = await requireOK(await fetch(`${baseURL}/requests`), "Failed to load the agent's requests");
      const data = (await response.json()) as {requests?: NativeRequest[]};
      return data.requests ?? [];
    },

    async respondRequest(id: string, body: RequestResponse): Promise<NativeRequest> {
      const response = await fetch(`${baseURL}/requests/${encodeURIComponent(id)}/respond`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        throw new RespondError(await errorDetail(response, "Failed to answer the agent"), response.status);
      }
      const data = (await response.json()) as NativeRequest & {$schema?: string};
      delete data.$schema;
      return data;
    },

    async steerQueuedMessage(id: number): Promise<void> {
      const response = await fetch(`${baseURL}/queue/${id}/steer`, {method: "POST"});
      if (!response.ok) throw new Error(await errorDetail(response, "Failed to add the task to the current turn"));
    },

    async reorderQueue(ids: number[]): Promise<void> {
      const response = await fetch(`${baseURL}/queue`, {
        method: "PUT",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({ids}),
      });
      if (!response.ok) {
        throw new Error(response.status === 409
          ? "The queue changed in the meantime; try again"
          : await errorDetail(response, "Failed to reorder the queue"));
      }
    },

    uploadFiles(
      formData: FormData,
      options: UploadOptions = {},
    ): Promise<FileUploadResponse> {
      const send = (signInOn401: boolean) => new Promise<FileUploadResponse>((resolve) => {
        const request = new XMLHttpRequest();
        request.open("POST", `${baseURL}/upload`);
        request.withCredentials = withCredentials(baseURL);
        request.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            options.onProgress?.(
              Math.min(100, Math.round((event.loaded / event.total) * 100)),
            );
          }
        };
        request.onload = () => {
          if (request.status === 401 && signInOn401 && !options.signal?.aborted) {
            // Ask for the API token, then upload again.
            markAuthRequired(baseURL);
            void requestSignIn(baseURL).then((signedIn) => signedIn
              ? send(false).then(resolve)
              : resolve({ok: false, error: "Sign in to upload files."}));
            return;
          }
          try {
            const data = JSON.parse(request.responseText) as
              | FileUploadResponse
              | APIErrorModel;
            if (request.status >= 200 && request.status < 300) {
              options.onProgress?.(100);
              resolve(data as FileUploadResponse);
              return;
            }
            resolve({
              ok: false,
              error: "detail" in data ? data.detail : "The upload was rejected.",
            });
          } catch {
            resolve({
              ok: false,
              error: "The server returned an invalid response.",
            });
          }
        };
        request.onerror = () =>
          resolve({ok: false, error: "The upload connection failed."});
        request.onabort = () =>
          resolve({ok: false, error: "Upload cancelled."});
        options.signal?.addEventListener("abort", () => request.abort(), {
          once: true,
        });
        request.send(formData);
      });
      return send(true);
    },

    async updateQueuedMessage(id: number, content: string) {
      await requireOK(
        await fetch(`${baseURL}/queue/${id}`, {
          method: "PUT",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({content}),
        }),
        "Failed to update queued message",
      );
    },

    async deleteQueuedMessage(id: number) {
      await requireOK(
        await fetch(`${baseURL}/queue/${id}`, {method: "DELETE"}),
        "Failed to delete queued message",
      );
    },

    async getTimelineEvents(): Promise<unknown[]> {
      const response = await requireOK(
        await fetch(`${baseURL}/timeline`),
        "Failed to export the current session",
      );
      const data = (await response.json()) as {events?: unknown[]};
      if (!Array.isArray(data.events)) {
        throw new Error("The server returned an invalid timeline response");
      }
      return data.events;
    },
  };
}
