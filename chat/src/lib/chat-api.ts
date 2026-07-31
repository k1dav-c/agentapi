import type {
  FileUploadResponse,
  QueuedMessage,
  SendResult,
} from "@/components/chat-provider";

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

export interface WebhookConfig {
  url: string;
  timeout_seconds: number;
  max_attempts: number;
  payload_template: string;
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

export function createChatAPI(baseURL: string) {
  return {
    async getWebhook(): Promise<WebhookConfig> {
      const response = await requireOK(
        await fetch(`${baseURL}/webhook`),
        "Webhook configuration is unavailable",
      );
      return (await response.json()) as WebhookConfig;
    },

    async updateWebhook(config: {
      url: string;
      timeout_seconds: number;
      max_attempts: number;
      payload_template?: string;
    }): Promise<WebhookConfig> {
      const response = await requireOK(
        await fetch(`${baseURL}/webhook`, {
          method: "PUT",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify(config),
        }),
        "Failed to update webhook configuration",
      );
      return (await response.json()) as WebhookConfig;
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

    async sendMessage(
      content: string,
      type: "user" | "raw",
    ): Promise<SendResult> {
      const response = await fetch(`${baseURL}/message`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({content, type}),
      });
      if (!response.ok) {
        const error = (await response.json()) as APIErrorModel;
        const details = error.errors?.map((item) => item.message).join(", ");
        throw new Error(details ? `${error.detail}: ${details}` : error.detail);
      }
      const result = (await response.json()) as {
        ok?: boolean;
        queued?: boolean;
      };
      return {ok: result.ok === true, queued: result.queued === true};
    },

    uploadFiles(
      formData: FormData,
      options: UploadOptions = {},
    ): Promise<FileUploadResponse> {
      return new Promise((resolve) => {
        const request = new XMLHttpRequest();
        request.open("POST", `${baseURL}/upload`);
        request.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            options.onProgress?.(
              Math.min(100, Math.round((event.loaded / event.total) * 100)),
            );
          }
        };
        request.onload = () => {
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
