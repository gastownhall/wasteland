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
}

export interface BrowseResponse {
  items: WantedSummary[];
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
}

export interface ConnectSessionResponse {
  token: string;
  integration_id: string;
}

export interface ConnectInput {
  connection_id: string;
  rig_handle: string;
  fork_org: string;
  fork_db: string;
  upstream: string;
  mode?: string;
}

export interface JoinInput {
  fork_org: string;
  fork_db: string;
  upstream: string;
  mode?: string;
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

export interface ProfileResponse {
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
  stamp_count: number;
  languages?: ProfileSkillEntry[];
  domains?: ProfileSkillEntry[];
  capabilities?: ProfileSkillEntry[];
  notable_projects?: ProfileProject[];
}

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
  top_skills?: string[];
}

export interface ScoreboardResponse {
  entries: ScoreboardEntry[];
  updated_at: string;
}
