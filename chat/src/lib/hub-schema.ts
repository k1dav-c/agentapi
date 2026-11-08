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
        DispatchAssign: {
            agent?: string;
            host?: string;
        };
        DispatchCost: {
            /** Format: int64 */
            calls: number;
            /** Format: int64 */
            tasks: number;
            tokens: components["schemas"]["DispatchTokens"];
            /** Format: double */
            usd: number;
            /** Format: double */
            work_seconds: number;
        };
        DispatchCreateIssueRequest: {
            assign?: components["schemas"]["DispatchAssign"];
            body: string;
            labels?: string[] | null;
            repo: string;
            title: string;
        };
        DispatchCreateIssueResponse: {
            issue: components["schemas"]["GitHubIssue"];
            job?: components["schemas"]["DispatchJob"];
        };
        DispatchError: {
            code: string;
            message: string;
        };
        DispatchGitHubStatus: {
            connected: boolean;
            html_url?: string;
            install_url?: string;
            installations: components["schemas"]["GitHubInstallation"][] | null;
            owner?: string;
            slug?: string;
        };
        DispatchHost: {
            /** Format: int64 */
            active: number;
            agentapi_version: string;
            agents: string[] | null;
            clone: boolean;
            credentials_available: boolean;
            default_agent: string;
            /** Format: int64 */
            heartbeat_seconds: number;
            hostname: string;
            hub_credentials: boolean;
            jobs: components["schemas"]["LauncherJobInfo"][] | null;
            /** Format: date-time */
            last_seen: string;
            /** Format: int64 */
            max_jobs: number;
            online: boolean;
            repos: string[] | null;
            stopped?: boolean;
            url: string;
        };
        DispatchHostsBody: {
            hosts: components["schemas"]["DispatchHost"][] | null;
        };
        DispatchIssuesBody: {
            issues: components["schemas"]["GitHubIssue"][] | null;
        };
        DispatchJob: {
            agent: string;
            /** Format: int64 */
            cost_comment_id?: number;
            /** Format: date-time */
            created_at: string;
            credentials: boolean;
            default_branch?: string;
            error?: string;
            host: string;
            instance_id: string;
            /** Format: int64 */
            issue: number;
            issue_url: string;
            key: string;
            pr_url?: string;
            repo: string;
            /** Format: int64 */
            round: number;
            runs: components["schemas"]["DispatchRun"][] | null;
            /** @enum {string} */
            state: "starting" | "working" | "review" | "failed" | "done" | "stopped";
            title?: string;
            /** Format: date-time */
            updated_at: string;
        };
        DispatchJobsBody: {
            jobs: components["schemas"]["DispatchJob"][] | null;
        };
        DispatchReposBody: {
            repos: components["schemas"]["GitHubRepo"][] | null;
        };
        DispatchRequest: {
            /** @enum {string} */
            action: "start" | "send" | "link_pr" | "stop";
            agent?: string;
            author?: string;
            comments?: components["schemas"]["DispatchReviewComment"][] | null;
            delivery_id?: string;
            dispatch_mode?: string;
            host?: string;
            /** Format: int64 */
            issue: number;
            message?: string;
            pr_url?: string;
            replace?: boolean;
            repo: string;
            review_state?: string;
        };
        DispatchResponse: {
            created: boolean;
            duplicate?: boolean;
            job: components["schemas"]["DispatchJob"];
        };
        DispatchReviewComment: {
            body: string;
            /** Format: int64 */
            line?: number;
            path: string;
        };
        DispatchRun: {
            agent: string;
            cost: components["schemas"]["DispatchCost"];
            end_reason?: string;
            /** Format: date-time */
            ended_at?: string;
            host: string;
            instance_id: string;
            missed?: boolean;
            /** Format: int64 */
            round: number;
            /** Format: date-time */
            started_at: string;
        };
        DispatchTokens: {
            /** Format: int64 */
            cache_read: number;
            /** Format: int64 */
            cache_write: number;
            /** Format: int64 */
            input: number;
            /** Format: int64 */
            output: number;
        };
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
        ForgetBody: {
            ids: string[];
        };
        GitHubInstallation: {
            account: string;
            /** Format: int64 */
            id: number;
            repository_selection: string;
        };
        GitHubIssue: {
            body: string;
            html_url: string;
            labels: components["schemas"]["GitHubLabel"][] | null;
            /** Format: int64 */
            number: number;
            pull_request?: components["schemas"]["GitHubPullRequestRef"];
            state: string;
            title: string;
            user: components["schemas"]["GitHubUser"];
        };
        GitHubLabel: {
            name: string;
        };
        GitHubPullRequestRef: {
            url?: string;
        };
        GitHubRepo: {
            default_branch: string;
            full_name: string;
            html_url: string;
            private: boolean;
        };
        GitHubUser: {
            login: string;
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
        LauncherJobInfo: {
            agent: string;
            branch: string;
            credentials?: string;
            default_branch: string;
            error?: string;
            instance_id: string;
            key: string;
            /** Format: int64 */
            pid?: number;
            /** Format: int64 */
            port: number;
            /** Format: int64 */
            run: number;
            /** Format: date-time */
            started_at: string;
            state: string;
            worktree: string;
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
