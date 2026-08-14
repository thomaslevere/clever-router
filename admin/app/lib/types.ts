export type EnvVariable = {
  key: string;
  value: string;
  is_secret: boolean;
};

export type Router = {
  id: string;
  slug: string;
  name: string;
  adapter_type: string;
  image_ref: string;
  desired_version: string;
  current_version: string;
  endpoint_path: string;
  native_panel_url: string;
  desired_state: string;
  runtime_state: string;
  target_addr: string;
  container_id: string;
  config: Record<string, any>;
  env_vars?: EnvVariable[];
  auto_restart_on_env_change?: boolean;
  providers_count: number;
  models_count: number;
  health_status: string;
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
};

export type Model = {
  id: string;
  router_id: string;
  model_id: string;
  provider: string;
  modalities: string;
  last_seen_at: string;
};

export type VirtualKey = {
  id: string;
  name: string;
  prefix: string;
  budget_cents: number;
  spent_cents: number;
  rate_limit_rpm: number;
  model_allowlist: string[];
  router_allowlist: string[];
  status: string;
  last_used_at: string | null;
  created_at: string;
};

export type AuditEntry = {
  id: number;
  actor: string;
  action: string;
  target_type: string;
  target_id: string;
  after: Record<string, any> | null;
  ts: string;
};

export type Credential = {
  id: string;
  provider: string;
  metadata: Record<string, any>;
  last_verified_at: string | null;
  created_at: string;
};
