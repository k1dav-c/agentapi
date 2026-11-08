/**
 * Generated from lib/hub/hub-schema.json by scripts/hub-schema.ts (bun run
 * gen:api). Do not make direct changes to the file.
 */

export type paths = Record<string, never>;
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        AddInstanceBody: {
            token?: string;
            url: string;
        };
        /**
         * Connection
         * @example connecting
         * @enum {string}
         */
        Connection: "connecting" | "connected" | "unreachable" | "stopped";
        ErrorBody: {
            error: string;
        };
        Favorite: {
            id: string;
            text: string;
            title?: string;
        };
        FavoritesBody: {
            favorites: components["schemas"]["Favorite"][];
        };
        HeartbeatEvent: {
            /** Format: date-time */
            time: string;
        };
        HubEvents: {
            favorites: components["schemas"]["FavoritesBody"];
            heartbeat: components["schemas"]["HeartbeatEvent"];
            instance: components["schemas"]["Instance"];
            instances: components["schemas"]["InstanceList"];
            removed: components["schemas"]["RemovedEvent"];
        };
        /** @description The limits of what the hub takes; the page's generated types have their values (hubLimits). */
        HubLimits: {
            /**
             * @description The longest favorite ID.
             * @constant
             */
            max_favorite_id: 64;
            /**
             * @description The longest favorite text, in characters.
             * @constant
             */
            max_favorite_text: 20000;
            /**
             * @description The longest favorite title, in characters.
             * @constant
             */
            max_favorite_title: 100;
            /**
             * @description The most favorites the hub keeps.
             * @constant
             */
            max_favorites: 100;
            /**
             * @description The longest group name, in characters.
             * @constant
             */
            max_group_name: 40;
        };
        Instance: {
            agent_type: string;
            agentapi_version?: string;
            /** Format: date-time */
            changed_at?: string;
            connection: components["schemas"]["Connection"];
            cwd?: string;
            error?: string;
            group?: string;
            /** Format: int64 */
            heartbeat_seconds?: number;
            hostname?: string;
            id: string;
            /** Format: date-time */
            last_seen: string;
            muted?: string;
            name?: string;
            /** Format: int64 */
            pid?: number;
            /** Format: int64 */
            pin?: number;
            /** Format: int64 */
            port?: number;
            server_id: string;
            source: components["schemas"]["Source"];
            state: components["schemas"]["State"];
            /** @description The instance's latest summary: SummaryBody of the AgentAPI's schema (openapi.json). */
            summary?: {
                [key: string]: unknown;
            };
            transport: components["schemas"]["Transport"];
            url?: string;
            work?: components["schemas"]["WorkTime"];
            workspace_url?: string;
        };
        InstanceChange: {
            group?: string;
            muted?: boolean;
            pinned?: boolean;
        };
        InstanceList: {
            hub_version: string;
            instances: components["schemas"]["Instance"][];
        };
        JoinInfo: {
            secret?: string;
            secret_shown: boolean;
            urls: components["schemas"]["JoinURL"][];
        };
        JoinURL: {
            interface?: string;
            url: string;
        };
        PinsBody: {
            ids: string[];
        };
        PinsConflict: {
            error: string;
            ids: string[];
        };
        RemovedEvent: {
            id: string;
        };
        SessionBody: {
            authenticated: boolean;
        };
        SignInBody: {
            token: string;
        };
        /**
         * Source
         * @example webhook
         * @enum {string}
         */
        Source: "webhook" | "manual";
        /**
         * State
         * @example needs-you
         * @enum {string}
         */
        State: "needs-you" | "working" | "unread" | "idle" | "muted" | "connecting" | "unreachable" | "stopped";
        /**
         * Transport
         * @example native
         * @enum {string}
         */
        Transport: "native";
        WorkTime: {
            /** Format: int64 */
            last_24h_seconds: number;
            /** Format: int64 */
            last_7d_seconds: number;
            /** Format: date-time */
            working_since?: string;
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export type operations = Record<string, never>;

// hubLimits are the values of HubLimits: the limits of what the hub takes.
export const hubLimits = {
  max_favorite_id: 64,
  max_favorite_text: 20000,
  max_favorite_title: 100,
  max_favorites: 100,
  max_group_name: 40,
} as const satisfies components["schemas"]["HubLimits"];
