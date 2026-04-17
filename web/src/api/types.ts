export interface PendingItemSummary {
  rig_handle: string;
  status?: string;
  pr_url?: string;
  branch_url?: string;
}

export interface WantedSummary {
  id: string;
  title: string;
  project?: string;
  type?: string;
  priority: number;
  posted_by?: string;
  claimed_by?: string;
  status: string;
  effort_level: string;
  pending_count?: number;
  pending_items?: PendingItemSummary[];
}

export interface BrowseResponse {
  items: WantedSummary[];
  warning?: string;
}

export interface WantedItem {
  id: string;
  title: string;
  description?: string;
  project?: string;
  type?: string;
  priority: number;
  tags?: string[];
  posted_by?: string;
  claimed_by?: string;
  status: string;
  effort_level: string;
  created_at?: string;
  updated_at?: string;
}

export interface Completion {
  id: string;
  wanted_id: string;
  completed_by: string;
  evidence?: string;
  stamp_id?: string;
  validated_by?: string;
}

export interface Stamp {
  id: string;
  author: string;
  subject: string;
  quality: number;
  reliability: number;
  severity: string;
  context_id?: string;
  context_type?: string;
  skill_tags?: string[];
  message?: string;
}

export interface UpstreamPR {
  is_upstream: boolean;
  rig_handle: string;
  status: string;
  claimed_by?: string;
  branch?: string;
  branch_url?: string;
  pr_url?: string;
  delta?: string;
  completed_by?: string;
  evidence?: string;
}

export interface DetailResponse {
  item: WantedItem;
  completion?: Completion;
  stamp?: Stamp;
  branch?: string;
  branch_url?: string;
  main_status?: string;
  pr_url?: string;
  delta?: string;
  actions: string[];
  branch_actions: string[];
  mode: string;
  upstream_prs?: UpstreamPR[];
}

export interface MutationResponse {
  detail?: DetailResponse;
  branch?: string;
  hint?: string;
}

export interface DashboardResponse {
  claimed: WantedSummary[];
  in_review: WantedSummary[];
  completed: WantedSummary[];
}

export interface UpstreamInfo {
  upstream: string;
  fork_org: string;
  fork_db: string;
  mode: string;
}

export interface ConfigResponse {
  rig_handle: string;
  mode: string;
  hosted?: boolean;
  connected?: boolean;
  upstream?: string;
  upstreams?: UpstreamInfo[];
}

export interface RuntimeConfigResponse {
  environment?: string;
  browser_tracing_enabled: boolean;
  browser_trace_endpoint?: string;
  browser_trace_sample_ratio?: number;
}

export interface BootstrapResponse {
  authenticated: boolean;
  connected: boolean;
  hosted?: boolean;
  rig_handle?: string;
  wastelands?: WastelandConfig[];
  environment?: string;
  active_upstream?: string;
  mode?: string;
}

export interface WastelandConfig {
  upstream: string;
  fork_org: string;
  fork_db: string;
  mode: string;
  signing: boolean;
}

export interface AuthStatusResponse {
  authenticated: boolean;
  connected: boolean;
  rig_handle?: string;
  wastelands?: WastelandConfig[];
  environment?: string;
}

export interface ConnectSessionInput {
  rig_handle: string;
  fork_org: string;
  fork_db: string;
  upstream: string;
  mode?: string;
  signing?: boolean;
  display_name?: string;
  email?: string;
}

export interface ConnectSessionResponse {
  auth_service_base_url: string;
  connect_token: string;
  redeem_secret: string;
  expires_at: string;
}

export interface ConnectTokenMetadataWasteland {
  upstream: string;
  fork_org: string;
  fork_db: string;
  mode: string;
  signing: boolean;
}

export interface ConnectTokenMetadata {
  rig_handle: string;
  wastelands: ConnectTokenMetadataWasteland[];
}

export interface RedeemConnectTokenInput {
  auth_service_base_url: string;
  connect_token: string;
  redeem_secret: string;
  api_key: string;
  metadata: ConnectTokenMetadata;
}

export interface RedeemConnectTokenResponse {
  connection_id: string;
  status: string;
  last_validated_at?: string;
}

export interface RedeemConnectTokenErrorResponse {
  error_code?: string;
  user_message?: string;
  retryable?: boolean;
  request_id?: string;
}

export interface ConnectInput {
  connection_id: string;
  upstream?: string;
  display_name?: string;
  email?: string;
}

export interface ConnectResponse {
  status: string;
  setup_warning?: string;
}

export interface JoinInput {
  fork_org: string;
  fork_db: string;
  upstream: string;
  mode?: string;
  signing?: boolean;
  display_name?: string;
  email?: string;
}

export interface JoinResponse {
  status: string;
  setup_warning?: string;
}

export interface ErrorResponse {
  error: string;
}

export interface BrowseFilter {
  status?: string;
  type?: string;
  priority?: number;
  project?: string;
  search?: string;
  sort?: string;
  limit?: number;
  view?: string;
}

export interface PostInput {
  title: string;
  description?: string;
  project?: string;
  type?: string;
  priority?: number;
  effort_level?: string;
  tags?: string[];
}

export interface UpdateInput {
  title?: string;
  description?: string;
  project?: string;
  type?: string;
  priority?: number;
  effort_level?: string;
  tags?: string[];
  tags_set?: boolean;
}

export interface SettingsInput {
  mode: string;
  signing: boolean;
}

export interface ProfileSkillEntry {
  name: string;
  quality: number;
  reliability: number;
  creativity: number;
  confidence: number;
  message: string;
}

export interface ProfileProject {
  name: string;
  stars: number;
  languages?: string[];
  role?: string;
  impact_tier?: string;
}

export interface CharacterSheetResponse {
  kind: "character_sheet";
  handle: string;
  display_name: string;
  bio?: string;
  location?: string;
  company?: string;
  avatar_url?: string;
  source: string;
  confidence: number;
  created_at: string;
  total_repos?: number;
  total_stars?: number;
  followers?: number;
  account_age?: number;
  quality: number;
  reliability: number;
  creativity: number;
  assessment_count: number;
  languages?: ProfileSkillEntry[];
  domains?: ProfileSkillEntry[];
  capabilities?: ProfileSkillEntry[];
  notable_projects?: ProfileProject[];
}

export interface StampFeedEntry {
  id: string;
  skill_tags: string[];
  quality: number;
  reliability: number;
  validator: string;
  message?: string;
  evidence_url?: string;
  evidence_label?: string;
  evidence_text?: string;
  created_at: string;
}

export interface StampFeedResponse {
  kind: "stamp_feed";
  handle: string;
  github_url: string;
  stamps: StampFeedEntry[];
  stamps_error: string | null;
}

export type ProfileResponse = CharacterSheetResponse | StampFeedResponse;

export interface ProfileSummary {
  handle: string;
  display_name: string;
}

export interface ScoreboardEntry {
  rig_handle: string;
  display_name?: string;
  trust_tier: string;
  stamp_count: number;
  weighted_score: number;
  unique_towns: number;
  completions: number;
  avg_quality: number;
  avg_reliability: number;
  avg_creativity: number;
  top_skills?: string[];
}

export interface ScoreboardResponse {
  entries: ScoreboardEntry[];
  updated_at: string;
}
