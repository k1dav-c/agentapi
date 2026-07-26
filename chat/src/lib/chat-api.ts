import type {
  BackgroundTask,
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

    async getQueue(): Promise<QueuedMessage[]> {
      const response = await fetch(`${baseURL}/queue`);
      if (!response.ok) return [];
      const data = (await response.json()) as {messages?: QueuedMessage[]};
      return data.messages ?? [];
    },

    async getBackgroundTasks(): Promise<BackgroundTask[]> {
      const response = await fetch(`${baseURL}/background-tasks`);
      if (!response.ok) return [];
      const data = (await response.json()) as {tasks?: BackgroundTask[]};
      return data.tasks ?? [];
    },

    async getBackgroundTaskOutput(id: string) {
      const response = await requireOK(
        await fetch(
          `${baseURL}/background-tasks/${encodeURIComponent(id)}/output`,
        ),
        "Background task output is unavailable",
      );
      return (await response.json()) as {
        content: string;
        path: string;
        size: number;
        truncated: boolean;
      };
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
