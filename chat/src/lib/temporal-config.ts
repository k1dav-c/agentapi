export interface TemporalConfig {
  agent_url: string;
  temporal_address: string;
  namespace: string;
  task_queue: string;
  temporal_tls: boolean;
  channel_id: string;
  allowed_users: string[];
  agent_token_env: string;
  temporal_api_key_env: string;
  bot_token_env: string;
  agent_token?: string;
  temporal_api_key?: string;
  bot_token?: string;
}

export interface TemporalConfigResponse {
  config: TemporalConfig;
  path: string;
}

export interface TemporalProfilesResponse {
  profiles: Record<string, TemporalConfig>;
  path: string;
}
