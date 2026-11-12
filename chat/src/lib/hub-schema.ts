/**
 * Generated from lib/hub/hub-schema.json by scripts/hub-schema.ts (bun run
 * gen:api). Do not make direct changes to the file.
 */

export type paths = Record<string, never>;
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        ActivityBody: {
            days: components["schemas"]["ActivityDay"][];
            /** @description The first day the hub kept its activity, as YYYY-MM-DD in the time zone asked for: the days before it aren't counted. */
            since: string;
            /** @description The time zone of the days. */
            time_zone: string;
        };
        ActivityDay: {
            /** @description The day, as YYYY-MM-DD. */
            date: string;
            /** @description The tokens of the calls usd adds up. */
            tokens: components["schemas"]["SummaryTokens"];
            /**
             * Format: double
             * @description What the agents' sessions spent that day, API-equivalent at list prices; each scope once. AgentAPIs before 4.2 don't count.
             */
            usd: number;
            /**
             * Format: int64
             * @description How long the agents worked that day, as the hub saw it, each one's time added up.
             */
            work_seconds: number;
        };
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
        /**
         * DispatchAction
         * @example start
         * @enum {string}
         */
        DispatchAction: "start" | "send" | "link_pr" | "stop";
        DispatchAssign: {
            agent?: string;
            host?: string;
        };
        DispatchBoard: {
            hosts: components["schemas"]["DispatchHost"][] | null;
            jobs: components["schemas"]["DispatchJob"][] | null;
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
        DispatchGitHubStatus: {
            connected: boolean;
            html_url?: string;
            install_url?: string;
            installations: components["schemas"]["DispatchInstallationStatus"][] | null;
            owner?: string;
            owner_type?: string;
            settings_url?: string;
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
        DispatchInstallationStatus: {
            account: string;
            account_type?: string;
            avatar_url: string;
            /** Format: int64 */
            id: number;
            repository_selection: string;
            settings_url?: string;
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
            messages?: components["schemas"]["DispatchMessage"][] | null;
            pr_url?: string;
            repo: string;
            /** Format: int64 */
            round: number;
            runs: components["schemas"]["DispatchRun"][] | null;
            state: components["schemas"]["DispatchJobState"];
            title?: string;
            /** Format: date-time */
            updated_at: string;
        };
        /**
         * DispatchJobState
         * @example starting
         * @enum {string}
         */
        DispatchJobState: "starting" | "working" | "review" | "failed" | "done" | "stopped";
        DispatchJobsBody: {
            jobs: components["schemas"]["DispatchJob"][] | null;
        };
        DispatchMessage: {
            agent: string;
            /** Format: date-time */
            at: string;
            author?: string;
            /** Format: int64 */
            comments?: number;
            review_state?: string;
            source: components["schemas"]["DispatchMessageSource"];
            text: string;
        };
        /**
         * DispatchMessageSource
         * @example hub
         * @enum {string}
         */
        DispatchMessageSource: "hub" | "review" | "comment";
        DispatchReposBody: {
            repos: components["schemas"]["GitHubRepo"][] | null;
        };
        DispatchRequest: {
            action: components["schemas"]["DispatchAction"];
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
            server_id?: string;
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
            code: string;
            error: string;
            message: string;
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
            /** Format: date-time */
            pushed_at?: string;
            /** Format: date-time */
            updated_at?: string;
        };
        GitHubUser: {
            login: string;
        };
        HeartbeatEvent: {
            /** Format: date-time */
            time: string;
        };
        HubEvents: {
            dispatch: components["schemas"]["DispatchBoard"];
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
            /**
             * @description The most model providers the hub keeps.
             * @constant
             */
            max_providers: 50;
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
            server_id?: string;
            /** Format: date-time */
            started_at: string;
            state: components["schemas"]["LauncherJobState"];
            worktree: string;
        };
        /**
         * LauncherJobState
         * @example starting
         * @enum {string}
         */
        LauncherJobState: "starting" | "running" | "restarting" | "failed" | "stopping";
        Model: {
            /** @description Model ID the provider's API takes. */
            id: string;
            /** @description Name to show; the ID when empty. */
            name?: string;
        };
        PinsBody: {
            ids: string[];
        };
        PinsConflict: {
            code: string;
            error: string;
            ids: string[];
            message: string;
        };
        ProviderChange: {
            /** @description Base URL for Claude Code when it isn't base_url without /v1. */
            anthropic_base_url?: string;
            /** @description The provider's API key; missing keeps it at unchanged endpoints, empty removes it. */
            api_key?: string;
            /** @description Base URL of the provider's API, e.g. https://gateway.example.com/v1. Codex calls <base_url>/responses; Claude Code calls the same URL without /v1, plus /v1/messages. */
            base_url: string;
            /** @description Identifies the provider: lowercase letters, digits, - and _. */
            id: string;
            /** @description Models to offer; when empty, those the provider lists at <base_url>/models. */
            models?: components["schemas"]["Model"][];
            /** @description Name to show; the ID when empty. */
            name?: string;
        };
        ProviderCheck: {
            /** @description Base URL for Claude Code when it isn't base_url without /v1. */
            anthropic_base_url?: string;
            /** @description The key to check with; missing uses the saved one at unchanged endpoints. */
            api_key?: string;
            /** @description Base URL of the provider's API, e.g. https://gateway.example.com/v1. Codex calls <base_url>/responses; Claude Code calls the same URL without /v1, plus /v1/messages. */
            base_url: string;
            /** @description Identifies the provider: lowercase letters, digits, - and _. */
            id: string;
            /** @description Models to offer; when empty, those the provider lists at <base_url>/models. */
            models?: components["schemas"]["Model"][];
            /** @description Name to show; the ID when empty. */
            name?: string;
        };
        ProviderCheckResult: {
            /**
             * Format: int64
             * @description How long the provider took to answer, in milliseconds.
             */
            latency_ms: number;
            /** @description The models the provider lists at <base_url>/models. */
            models: components["schemas"]["Model"][];
        };
        ProviderView: {
            /** @description Base URL for Claude Code when it isn't base_url without /v1. */
            anthropic_base_url?: string;
            /** @description Base URL of the provider's API, e.g. https://gateway.example.com/v1. Codex calls <base_url>/responses; Claude Code calls the same URL without /v1, plus /v1/messages. */
            base_url: string;
            /** @description The provider has an API key, which the hub doesn't show. */
            has_key: boolean;
            /** @description Identifies the provider: lowercase letters, digits, - and _. */
            id: string;
            /** @description Models to offer; when empty, those the provider lists at <base_url>/models. */
            models?: components["schemas"]["Model"][];
            /** @description Name to show; the ID when empty. */
            name?: string;
        };
        ProvidersBody: {
            providers: components["schemas"]["ProviderView"][];
            /** @description The hub's opaque optimistic-concurrency token for the provider list. */
            revision: string;
        };
        ProvidersChange: {
            providers: components["schemas"]["ProviderChange"][];
            /** @description The revision returned by the client's latest provider-list read. */
            revision: string;
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
        SummaryTokens: {
            /**
             * Format: int64
             * @description Input tokens read from the prompt cache.
             */
            cache_read: number;
            /**
             * Format: int64
             * @description Input tokens written to the prompt cache, the five-minute and one-hour caches together.
             */
            cache_write: number;
            /**
             * Format: int64
             * @description Uncached input tokens.
             */
            input: number;
            /**
             * Format: int64
             * @description Output tokens, thinking included.
             */
            output: number;
            /**
             * Format: int64
             * @description The sum of input, output, cache_write and cache_read.
             */
            total: number;
        };
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
  max_providers: 50,
} as const satisfies components["schemas"]["HubLimits"];
