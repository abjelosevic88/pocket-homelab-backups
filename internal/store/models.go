package store

// User is an account. Backups are box-wide: admins manage them, members
// only see whether the box is protected.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	FeedToken    string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Connection is a place outside the box: a cloud account or a server.
type Connection struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"` // gdrive | onedrive | dropbox | b2 | s3 | sftp | webdav | pocketfiles | rclone
	Config     ConnConfig `json:"config"`
	Secret     string     `json:"-"` // sealed ConnSecret JSON
	ActingUser *int64     `json:"acting_user"`
	Status     string     `json:"status"` // "" | ok | error
	LastError  string     `json:"last_error"`
	CheckedAt  string     `json:"checked_at"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
	HasSecret  bool       `json:"has_secret"`
}

// ConnConfig is what a connection may show: no keys, no passwords.
type ConnConfig struct {
	Host     string `json:"host,omitempty"`     // sftp
	Port     int    `json:"port,omitempty"`     // sftp
	User     string `json:"user,omitempty"`     // sftp, webdav
	KeyFile  string `json:"key_file,omitempty"` // sftp: a private key on the box
	URL      string `json:"url,omitempty"`      // webdav
	Vendor   string `json:"vendor,omitempty"`   // webdav: nextcloud | owncloud | other
	Bucket   string `json:"bucket,omitempty"`   // b2, s3
	Endpoint string `json:"endpoint,omitempty"` // s3
	Region   string `json:"region,omitempty"`   // s3
	Provider string `json:"provider,omitempty"` // s3: AWS | Minio | Wasabi | Cloudflare | Other …
	Account  string `json:"account,omitempty"`  // b2 key id, s3 access key id, signed-in email
	Root     string `json:"root,omitempty"`     // a folder every path is under
	Scope    string `json:"scope,omitempty"`    // gdrive: drive | drive.file | drive.readonly
	// Options are raw rclone options for kind rclone (and extra ones for any kind).
	Options map[string]string `json:"options,omitempty"`
	// Imported tells where the connection came from ("rclone.conf: gdrive-backup").
	Imported string `json:"imported,omitempty"`
}

// ConnSecret is sealed in the database.
type ConnSecret struct {
	Password     string `json:"password,omitempty"`      // sftp, webdav
	Key          string `json:"key,omitempty"`           // b2 application key, s3 secret key
	Token        string `json:"token,omitempty"`         // OAuth token JSON (rclone's format)
	ClientID     string `json:"client_id,omitempty"`     // OAuth client the token belongs to
	ClientSecret string `json:"client_secret,omitempty"` // (rclone needs it to refresh)
	PrivateKey   string `json:"private_key,omitempty"`   // sftp key pasted in
	// Options are secret raw rclone options (kind rclone).
	Options map[string]string `json:"options,omitempty"`
}

// Repository is a restic repository.
type Repository struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	Location      string      `json:"location"` // local | connection | usb | uri
	Path          string      `json:"path"`
	ConnectionID  *int64      `json:"connection_id"`
	Password      string      `json:"-"` // sealed
	PasswordFile  string      `json:"password_file"`
	Options       RepoOptions `json:"options"`
	ManagedBy     string      `json:"managed_by"` // pocket | backrest
	BackrestRepo  string      `json:"backrest_repo"`
	CheckSchedule string      `json:"check_schedule"`
	CheckReadPct  int         `json:"check_read_pct"`
	PruneSchedule string      `json:"prune_schedule"`
	CopiesPaused  bool        `json:"copies_paused"`
	PausedReason  string      `json:"paused_reason"`
	Stats         RepoStats   `json:"stats"`
	Status        string      `json:"status"` // "" | ok | error
	LastError     string      `json:"last_error"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
	HasPassword   bool        `json:"has_password"`
}

// RepoOptions are the less common settings of a repository.
type RepoOptions struct {
	Compression string   `json:"compression,omitempty"` // auto | max | off
	USB         *USBDisk `json:"usb,omitempty"`
	// Background runs restic at the lowest CPU and disk priority.
	Background bool `json:"background,omitempty"`
}

// USBDisk is a removable disk the repository lives on: it is mounted for
// each use and unmounted (and powered down) after, so it stays offline.
type USBDisk struct {
	UUID       string `json:"uuid"`
	Label      string `json:"label,omitempty"`
	Mountpoint string `json:"mountpoint"`
	PowerOff   bool   `json:"power_off"`
	WarnPct    int    `json:"warn_pct,omitempty"`
}

// RepoStats is what the repository looked like when last read.
type RepoStats struct {
	Size      int64  `json:"size"`
	Snapshots int    `json:"snapshots"`
	Latest    string `json:"latest"`
	DiskFree  int64  `json:"disk_free,omitempty"`
	DiskUsed  int    `json:"disk_used_pct,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

// Source is something to protect.
type Source struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Kind           string       `json:"kind"` // paths | sqlite | postgres | mariadb | volume | container_files | container_command | host_command
	Enabled        bool         `json:"enabled"`
	Config         SourceConfig `json:"config"`
	Secret         string       `json:"-"`
	Project        string       `json:"project"`
	Notes          string       `json:"notes"`
	SortOrder      float64      `json:"sort_order"`
	LastOKAt       string       `json:"last_ok_at"`
	LastError      string       `json:"last_error"`
	LastSize       int64        `json:"last_size"`
	LastDurationMS int64        `json:"last_duration_ms"`
	CreatedAt      string       `json:"created_at"`
	UpdatedAt      string       `json:"updated_at"`
	HasSecret      bool         `json:"has_secret"`
	Jobs           []int64      `json:"jobs"`
}

// SourceConfig holds the settings of every kind of source.
type SourceConfig struct {
	// paths
	Paths    []string `json:"paths,omitempty"`
	Excludes []string `json:"excludes,omitempty"`
	// sqlite: the database file; container_files: the folder in the container
	Path string `json:"path,omitempty"`
	// postgres, mariadb, container_*: the container
	Container string `json:"container,omitempty"`
	// postgres, mariadb: empty = read from the container's own environment
	Database    string `json:"database,omitempty"`
	User        string `json:"user,omitempty"`
	PasswordEnv string `json:"password_env,omitempty"` // a variable in the container that holds the password
	// postgres, mariadb: tables whose rows are left out (the schema stays)
	ExcludeTableData []string `json:"exclude_table_data,omitempty"`
	ExtraArgs        []string `json:"extra_args,omitempty"`
	// volume: a named Docker volume
	Volume string `json:"volume,omitempty"`
	// container_files: entries inside Path (empty = everything)
	Files []string `json:"files,omitempty"`
	// container_command, host_command
	Command string `json:"command,omitempty"`
	RunAs   string `json:"run_as,omitempty"` // host_command: user name or uid (default the app's uid)
	// Output is the file under the staging folder ("" = a name from the kind).
	Output string `json:"output,omitempty"`
	// Private writes the output 0600 (keys, password hashes).
	Private bool `json:"private,omitempty"`
	// Compress gzips the output.
	Compress bool `json:"compress,omitempty"`
	// SkipIfMissing makes a missing database file or stopped container a
	// skip instead of a failure.
	SkipIfMissing bool `json:"skip_if_missing,omitempty"`
	// OKExitCodes are exit codes that still count as success (tar's 1 =
	// "some files changed while being read").
	OKExitCodes []int `json:"ok_exit_codes,omitempty"`
}

// Job is a backup (or a cloud pull) with its schedule.
type Job struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`   // backup | pull
	Engine       string    `json:"engine"` // pocket | backrest
	BackrestPlan string    `json:"backrest_plan"`
	RepositoryID *int64    `json:"repository_id"`
	Schedule     string    `json:"schedule"`
	Enabled      bool      `json:"enabled"`
	Config       JobConfig `json:"config"`
	SortOrder    float64   `json:"sort_order"`
	CreatedAt    string    `json:"created_at"`
	UpdatedAt    string    `json:"updated_at"`
	SourceIDs    []int64   `json:"source_ids"`
	Copies       []Copy    `json:"copies"`
}

// JobConfig holds a job's settings.
type JobConfig struct {
	StagingDir string    `json:"staging_dir,omitempty"`
	Paths      []string  `json:"paths,omitempty"`
	Excludes   []string  `json:"excludes,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Retention  Retention `json:"retention"`
	// ForgetBeforeCopies applies retention (and prunes) before the copies so
	// they carry only what is kept.
	ForgetBeforeCopies bool `json:"forget_before_copies,omitempty"`
	// VerifyAfter reads the repository back after the snapshot.
	VerifyAfter bool `json:"verify_after,omitempty"`
	// RequireMounts are folders that must be mounted (a NAS share) or the
	// job refuses to run rather than back up an empty folder.
	RequireMounts []string `json:"require_mounts,omitempty"`
	HeartbeatURL  string   `json:"heartbeat_url,omitempty"`
	NotifySuccess bool     `json:"notify_success"`
	NotifyFailure bool     `json:"notify_failure"`
	// CoverageCheck warns before each run about containers no backup covers.
	CoverageCheck bool `json:"coverage_check,omitempty"`
	// Report regenerates the Homepage report after a good run.
	Report        bool `json:"report,omitempty"`
	ExcludeCaches bool `json:"exclude_caches,omitempty"`
	CheckReadPct  int  `json:"check_read_pct,omitempty"` // check after each run (offline disks)
	// RunOnConnect starts the job when its USB disk is plugged in (when the
	// last good run is at least RunOnConnectDays old).
	RunOnConnect     bool        `json:"run_on_connect,omitempty"`
	RunOnConnectDays int         `json:"run_on_connect_days,omitempty"`
	Pull             *PullConfig `json:"pull,omitempty"`
}

// Retention says which snapshots are kept.
type Retention struct {
	KeepAll     bool `json:"keep_all,omitempty"`
	KeepLast    int  `json:"keep_last,omitempty"`
	KeepHourly  int  `json:"keep_hourly,omitempty"`
	KeepDaily   int  `json:"keep_daily,omitempty"`
	KeepWeekly  int  `json:"keep_weekly,omitempty"`
	KeepMonthly int  `json:"keep_monthly,omitempty"`
	KeepYearly  int  `json:"keep_yearly,omitempty"`
	// GroupByNone counts snapshots across hosts and path sets (restic --group-by '').
	GroupByNone bool `json:"group_by_none,omitempty"`
	Prune       bool `json:"prune,omitempty"`
}

// Empty reports whether no retention is set (everything is kept).
func (r Retention) Empty() bool {
	return r.KeepAll || r.KeepLast+r.KeepHourly+r.KeepDaily+r.KeepWeekly+r.KeepMonthly+r.KeepYearly == 0
}

// PullConfig copies a folder of a connection home.
type PullConfig struct {
	ConnectionID int64  `json:"connection_id"`
	RemotePath   string `json:"remote_path"`
	LocalPath    string `json:"local_path"`
	// Mirror deletes local files that vanished upstream; off = copy (safer).
	Mirror bool `json:"mirror,omitempty"`
	// SharedWithMe also brings Google Drive's "Shared with me" to SharedPath.
	SharedWithMe  bool     `json:"shared_with_me,omitempty"`
	SharedPath    string   `json:"shared_path,omitempty"`
	ExportFormats string   `json:"export_formats,omitempty"` // gdrive: docx,xlsx,pptx,svg
	TPSLimit      float64  `json:"tps_limit,omitempty"`
	Transfers     int      `json:"transfers,omitempty"`
	ExtraFlags    []string `json:"extra_flags,omitempty"`
	RequireMount  bool     `json:"require_mount,omitempty"` // LocalPath must be a mount point
}

// Copy is where a job's repository goes after each snapshot.
type Copy struct {
	ID                 int64       `json:"id"`
	JobID              int64       `json:"job_id"`
	Name               string      `json:"name"`
	Mode               string      `json:"mode"` // mirror | restic
	ConnectionID       *int64      `json:"connection_id"`
	Path               string      `json:"path"`
	TargetRepositoryID *int64      `json:"target_repository_id"`
	Enabled            bool        `json:"enabled"`
	Options            CopyOptions `json:"options"`
	SortOrder          float64     `json:"sort_order"`
	LastOKAt           string      `json:"last_ok_at"`
	LastError          string      `json:"last_error"`
	LastDurationMS     int64       `json:"last_duration_ms"`
	CreatedAt          string      `json:"created_at"`
}

// CopyOptions tune a mirror.
type CopyOptions struct {
	Transfers    int    `json:"transfers,omitempty"`
	HardDelete   bool   `json:"hard_delete,omitempty"`   // b2: delete instead of hiding old versions
	DeleteBefore bool   `json:"delete_before,omitempty"` // free space first (quota-limited clouds)
	UseTrash     bool   `json:"use_trash,omitempty"`     // gdrive: send removed files to the trash
	BwLimit      string `json:"bwlimit,omitempty"`
}

// Run is one piece of work and how it went.
type Run struct {
	ID           int64  `json:"id"`
	JobID        *int64 `json:"job_id"`
	RepositoryID *int64 `json:"repository_id"`
	Kind         string `json:"kind"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"` // running | success | warning | failed | cancelled | skipped
	Title        string `json:"title"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	DurationMS   int64  `json:"duration_ms"`
	SnapshotID   string `json:"snapshot_id"`
	BytesAdded   int64  `json:"bytes_added"`
	BytesTotal   int64  `json:"bytes_total"`
	FilesNew     int64  `json:"files_new"`
	FilesChanged int64  `json:"files_changed"`
	Summary      string `json:"summary"`
	Error        string `json:"error"`
	Steps        []Step `json:"steps"`
	Log          string `json:"log,omitempty"`
	UserID       *int64 `json:"user_id"`
	JobName      string `json:"job_name,omitempty"`
	RepoName     string `json:"repository_name,omitempty"`
}

// Step is one part of a run.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warning | failed | skipped | running
	MS     int64  `json:"ms"`
	Detail string `json:"detail,omitempty"`
}

// CoverageSkip leaves a compose project out of the coverage check.
type CoverageSkip struct {
	Project   string `json:"project"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}
