// Shapes of the JSON API (see docs/api.md). Go: internal/store/models.go and internal/server/*.go.

export interface User {
  id: number
  email: string
  name: string
  username: string
  role: 'admin' | 'member'
  created_at: string
}

export interface AuthStatus {
  needs_setup: boolean
  core_configured?: boolean
  auth_mode: 'local' | 'proxy' | 'none'
  authenticated: boolean
  user: User | null
  version: string
  title: string
  oidc?: boolean
  oidc_name?: string
}

export interface Settings {
  title: string
  setup_complete: boolean
  time_format: '24h' | '12h'
  density: 'comfortable' | 'compact'
  notify_kind: 'apprise' | 'ntfy' | 'webhook'
  notify_url: string
  notify_failures: boolean
  notify_successes: boolean
}

export interface CoreStatus { configured: boolean; url: string; registered: boolean; error: string }

export interface SettingsResponse {
  settings: Settings
  notify_has_token: boolean
  notify_box_route: boolean
  core: CoreStatus
}

export interface ApiToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

/** GET /settings/system (About page). */
export interface SystemInfo {
  version: string
  go: string
  uptime: string
  auth_mode: string
  base_url: string
  timezone: string
  core: CoreStatus
  counts: Record<string, number>
  tools?: Record<string, string>
  host_access?: boolean
  backrest?: boolean
  restic_host?: string
  db_size?: number
  users?: number
  staging_dir?: string
  data_dir?: string
}

// ---------- connections ----------

export type ConnKindId = 'gdrive' | 'onedrive' | 'dropbox' | 'b2' | 's3' | 'sftp' | 'webdav' | 'pocketfiles' | 'rclone'

export interface ConnConfig {
  host?: string
  port?: number
  user?: string
  key_file?: string
  url?: string
  vendor?: string
  bucket?: string
  endpoint?: string
  region?: string
  provider?: string
  account?: string
  root?: string
  scope?: string
  options?: Record<string, string>
  imported?: string
}

export interface Connection {
  id: number
  name: string
  kind: ConnKindId
  config: ConnConfig
  acting_user: number | null
  status: '' | 'ok' | 'error'
  last_error: string
  checked_at: string
  created_at: string
  updated_at: string
  has_secret: boolean
}

export interface ConnField { key: string; label: string; secret?: boolean; placeholder?: string; help?: string; optional?: boolean }
export interface ConnKind { kind: ConnKindId; name: string; auth: 'oauth' | 'form' | 'core'; ready: boolean; help: string; fields: ConnField[] }
export interface ConnKinds { kinds: ConnKind[]; import_file: string }

export interface RemoteEntry { name: string; path: string; size: number; is_dir: boolean; mod_time: string }
export interface About { total: number; used: number; free: number }

export interface Importable { file: string; remotes: { name: string; type: string; kind: string; scope: string; imported: boolean }[] }

// ---------- repositories ----------

export interface USBDisk { uuid: string; label?: string; mountpoint: string; power_off: boolean; warn_pct?: number }
export interface RepoOptions { compression?: string; usb?: USBDisk; background?: boolean }
export interface RepoStats { size: number; snapshots: number; latest: string; disk_free?: number; disk_used_pct?: number; updated_at: string }

export interface Repository {
  id: number
  name: string
  location: 'local' | 'connection' | 'usb' | 'uri'
  path: string
  connection_id: number | null
  password_file: string
  options: RepoOptions
  managed_by: 'pocket' | 'backrest'
  backrest_repo: string
  check_schedule: string
  check_read_pct: number
  prune_schedule: string
  copies_paused: boolean
  paused_reason: string
  stats: RepoStats
  status: '' | 'ok' | 'error'
  last_error: string
  created_at: string
  updated_at: string
  has_password: boolean
  disk_connected?: boolean
  disk_mounted?: boolean
  uri: string
  jobs: number[]
}

export interface Snapshot {
  id: string
  short_id: string
  time: string
  hostname: string
  paths: string[]
  tags: string[] | null
  summary?: {
    files_new: number
    files_changed: number
    data_added: number
    data_added_packed: number
    total_files_processed: number
    total_bytes_processed: number
  }
}

export interface SnapNode { name: string; type: string; path: string; size: number; mode: number; mtime: string }

export interface ReportRow { name: string; bytes: number }
export interface Report { rows: ReportRow[]; repository_size: number; snapshots: number; at: string }

// ---------- sources ----------

export type SourceKind = 'paths' | 'sqlite' | 'postgres' | 'mariadb' | 'volume' | 'container_files' | 'container_command' | 'host_command'

export interface SourceConfig {
  paths?: string[]
  excludes?: string[]
  path?: string
  container?: string
  database?: string
  user?: string
  password_env?: string
  exclude_table_data?: string[]
  extra_args?: string[]
  volume?: string
  files?: string[]
  command?: string
  run_as?: string
  output?: string
  private?: boolean
  compress?: boolean
  skip_if_missing?: boolean
  ok_exit_codes?: number[]
}

export interface Source {
  id: number
  name: string
  kind: SourceKind
  enabled: boolean
  config: SourceConfig
  project: string
  notes: string
  sort_order: number
  last_ok_at: string
  last_error: string
  last_size: number
  last_duration_ms: number
  created_at: string
  updated_at: string
  has_secret: boolean
  jobs: number[]
  output: string
}

export interface Suggestion { kind: SourceKind; name: string; project: string; config: SourceConfig; why: string; exists: boolean; container: string }

// ---------- jobs ----------

export interface Retention {
  keep_all?: boolean
  keep_last?: number
  keep_hourly?: number
  keep_daily?: number
  keep_weekly?: number
  keep_monthly?: number
  keep_yearly?: number
  group_by_none?: boolean
  prune?: boolean
}

export interface PullConfig {
  connection_id: number
  remote_path: string
  local_path: string
  mirror?: boolean
  shared_with_me?: boolean
  shared_path?: string
  export_formats?: string
  tps_limit?: number
  transfers?: number
  extra_flags?: string[]
  require_mount?: boolean
}

export interface JobConfig {
  staging_dir?: string
  paths?: string[]
  excludes?: string[]
  tags?: string[]
  retention: Retention
  forget_before_copies?: boolean
  verify_after?: boolean
  require_mounts?: string[]
  heartbeat_url?: string
  notify_success: boolean
  notify_failure: boolean
  coverage_check?: boolean
  report?: boolean
  exclude_caches?: boolean
  check_read_pct?: number
  run_on_connect?: boolean
  run_on_connect_days?: number
  pull?: PullConfig | null
}

export interface CopyOptions { transfers?: number; hard_delete?: boolean; delete_before?: boolean; use_trash?: boolean; bwlimit?: string }

export interface Copy {
  id: number
  job_id: number
  name: string
  mode: 'mirror' | 'restic'
  connection_id: number | null
  path: string
  target_repository_id: number | null
  enabled: boolean
  options: CopyOptions
  sort_order: number
  last_ok_at: string
  last_error: string
  last_duration_ms: number
  created_at: string
}

export interface Job {
  id: number
  name: string
  kind: 'backup' | 'pull'
  engine: 'pocket' | 'backrest'
  backrest_plan: string
  repository_id: number | null
  schedule: string
  enabled: boolean
  config: JobConfig
  sort_order: number
  created_at: string
  updated_at: string
  source_ids: number[]
  copies: Copy[]
  next_run: string
  last_run: Run | null
  last_good: Run | null
  running: number | null
}

// ---------- runs ----------

export type RunStatus = 'running' | 'success' | 'warning' | 'failed' | 'cancelled' | 'skipped'

export interface Step { name: string; status: 'ok' | 'warning' | 'failed' | 'skipped' | 'running'; ms: number; detail?: string }

export interface Run {
  id: number
  job_id: number | null
  repository_id: number | null
  kind: string
  trigger: string
  status: RunStatus
  title: string
  started_at: string
  finished_at: string
  duration_ms: number
  snapshot_id: string
  bytes_added: number
  bytes_total: number
  files_new: number
  files_changed: number
  summary: string
  error: string
  steps: Step[]
  log?: string
  user_id: number | null
  job_name?: string
  repository_name?: string
}

export interface RunDetail { run: Run; step?: string; percent?: number }

// ---------- overview ----------

export interface Issue { level: 'warning' | 'error'; text: string; link?: string }
export type Health = 'ok' | 'warning' | 'error' | 'empty'

export interface Overview {
  health: Health
  issues: Issue[] | number
  last_backup?: string
  jobs?: Job[]
  repositories?: Repository[]
  recent_runs?: Run[]
  connections?: Connection[]
  active?: { run: Run; step: string; percent: number }[]
}

// ---------- coverage ----------

export interface Mount { type: string; source: string; name?: string; destination: string; rw: boolean }
export interface DContainer { name: string; image: string; project: string; service: string; working_dir: string; state: string; mounts: Mount[] }

export interface CoverageProject {
  project: string
  working_dir: string
  containers: DContainer[]
  status: 'covered' | 'skipped' | 'uncovered'
  reason: string
  by: string[]
  volumes: { name: string; covered: boolean }[]
}

export interface Coverage { projects: CoverageProject[]; covered: number; skipped: number; uncovered: number; error?: string }

// ---------- Backrest ----------

export interface BROp { id: string; kind: string; status: string; start: string; end: string; snapshot: string; message: string; data_added: string; errors: number }

export interface BRPlan {
  id: string
  repo: string
  cron: string
  next_run: string
  paths: string[]
  excludes: number
  job_id: number | null
  hooked: boolean
  old_scripts: string[]
  last_backup: BROp | null
  recent: BROp[]
  retention: Record<string, unknown> | null
}

export interface BackrestInfo {
  configured: boolean
  url: string
  instance?: string
  error?: string
  watch_error?: string
  plans: BRPlan[]
  repos: { id: string; uri: string; repository_id?: number }[]
}

export interface HookChange { kind: 'plan' | 'repo'; id: string; before: string[]; after: string[] }

// ---------- integrations ----------

export interface Integrations {
  report_html_path: string
  report_widgets_yaml: string
  report_repo_id: string
  report_last: string
  backrest: { configured: boolean; url: string }
  host_access: boolean
  staging_dir: string
  tools: Record<string, string>
}

export interface LocalEntry { name: string; path: string; is_dir: boolean; size: number; mount?: boolean }
