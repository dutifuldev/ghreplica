package gitindex

import "time"

type StructuralSearchRequest struct {
	CommitSHA         string         `json:"commit_sha,omitempty"`
	Ref               string         `json:"ref,omitempty"`
	PullRequestNumber int            `json:"pull_request_number,omitempty"`
	Language          string         `json:"language"`
	Rule              map[string]any `json:"rule"`
	Paths             []string       `json:"paths,omitempty"`
	ChangedFilesOnly  bool           `json:"changed_files_only,omitempty"`
	Limit             int            `json:"limit,omitempty"`
}

type StructuralSearchResponse struct {
	Repository        SearchRepository  `json:"repository"`
	ResolvedCommitSHA string            `json:"resolved_commit_sha"`
	ResolvedRef       string            `json:"resolved_ref,omitempty"`
	Matches           []StructuralMatch `json:"matches"`
	Truncated         bool              `json:"truncated"`
}

type SearchRepository struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

type StructuralMatch struct {
	Path          string                 `json:"path"`
	StartLine     int                    `json:"start_line"`
	StartColumn   int                    `json:"start_column"`
	EndLine       int                    `json:"end_line"`
	EndColumn     int                    `json:"end_column"`
	Text          string                 `json:"text"`
	MetaVariables StructuralMetaVariable `json:"meta_variables,omitempty"`
}

type StructuralMetaVariable struct {
	Single      map[string]string   `json:"single,omitempty"`
	Multi       map[string][]string `json:"multi,omitempty"`
	Transformed map[string]string   `json:"transformed,omitempty"`
}

type FileChange struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"`
	Status       string `json:"status"`
	FileKind     string `json:"file_kind"`
	IndexedAs    string `json:"indexed_as"`
	OldMode      string `json:"old_mode,omitempty"`
	NewMode      string `json:"new_mode,omitempty"`
	HeadBlobSHA  string `json:"head_blob_sha,omitempty"`
	BaseBlobSHA  string `json:"base_blob_sha,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Changes      int    `json:"changes"`
	Patch        string `json:"patch,omitempty"`
	Hunks        []Hunk `json:"hunks,omitempty"`
}

type Hunk struct {
	Index    int    `json:"index"`
	DiffHunk string `json:"diff_hunk"`
	OldStart int    `json:"old_start"`
	OldCount int    `json:"old_count"`
	OldEnd   int    `json:"old_end"`
	NewStart int    `json:"new_start"`
	NewCount int    `json:"new_count"`
	NewEnd   int    `json:"new_end"`
}

type PullRequestSnapshot struct {
	RepositoryID      uint       `json:"repository_id"`
	PullRequestNumber int        `json:"pull_request_number"`
	HeadSHA           string     `json:"head_sha"`
	BaseSHA           string     `json:"base_sha"`
	MergeBaseSHA      string     `json:"merge_base_sha"`
	BaseRef           string     `json:"base_ref"`
	State             string     `json:"state"`
	Draft             bool       `json:"draft"`
	IndexedAs         string     `json:"indexed_as"`
	IndexFreshness    string     `json:"index_freshness"`
	PathCount         int        `json:"path_count"`
	IndexedFileCount  int        `json:"indexed_file_count"`
	HunkCount         int        `json:"hunk_count"`
	Additions         int        `json:"additions"`
	Deletions         int        `json:"deletions"`
	PatchBytes        int        `json:"patch_bytes"`
	LastIndexedAt     *time.Time `json:"last_indexed_at,omitempty"`
}

type RepoStatus struct {
	RepositoryID                 uint       `json:"repository_id"`
	FullName                     string     `json:"full_name"`
	LastWebhookAt                *time.Time `json:"last_webhook_at,omitempty"`
	LastInventoryScanStartedAt   *time.Time `json:"last_inventory_scan_started_at,omitempty"`
	LastInventoryScanFinishedAt  *time.Time `json:"last_inventory_scan_finished_at,omitempty"`
	LastInventoryScanSucceededAt *time.Time `json:"last_inventory_scan_succeeded_at,omitempty"`
	LastBackfillStartedAt        *time.Time `json:"last_backfill_started_at,omitempty"`
	LastBackfillFinishedAt       *time.Time `json:"last_backfill_finished_at,omitempty"`
	BackfillMode                 string     `json:"backfill_mode"`
	BackfillPriority             int        `json:"backfill_priority"`
	TargetedRefreshPending       bool       `json:"targeted_refresh_pending"`
	TargetedRefreshRunning       bool       `json:"targeted_refresh_running"`
	InventoryGenerationCurrent   int        `json:"inventory_generation_current"`
	InventoryGenerationBuilding  *int       `json:"inventory_generation_building,omitempty"`
	InventoryNeedsRefresh        bool       `json:"inventory_needs_refresh"`
	InventoryLastCommittedAt     *time.Time `json:"inventory_last_committed_at,omitempty"`
	InventoryScanRunning         bool       `json:"inventory_scan_running"`
	BackfillRunning              bool       `json:"backfill_running"`
	BackfillGeneration           int        `json:"backfill_generation"`
	BackfillCursor               *int       `json:"backfill_cursor,omitempty"`
	BackfillCursorUpdatedAt      *time.Time `json:"backfill_cursor_updated_at,omitempty"`
	OpenPRTotal                  int        `json:"open_pr_total"`
	OpenPRCurrent                int        `json:"open_pr_current"`
	OpenPRStale                  int        `json:"open_pr_stale"`
	OpenPRMissing                int        `json:"open_pr_missing"`
	LastError                    string     `json:"last_error,omitempty"`
}

type PullRequestStatus struct {
	RepositoryID          uint       `json:"repository_id"`
	PullRequestNumber     int        `json:"pull_request_number"`
	State                 string     `json:"state,omitempty"`
	Draft                 bool       `json:"draft"`
	Indexed               bool       `json:"indexed"`
	HeadSHA               string     `json:"head_sha,omitempty"`
	BaseSHA               string     `json:"base_sha,omitempty"`
	MergeBaseSHA          string     `json:"merge_base_sha,omitempty"`
	BaseRef               string     `json:"base_ref,omitempty"`
	IndexedAs             string     `json:"indexed_as,omitempty"`
	IndexFreshness        string     `json:"index_freshness,omitempty"`
	LastIndexedAt         *time.Time `json:"last_indexed_at,omitempty"`
	ChangedFiles          int        `json:"changed_files"`
	IndexedFileCount      int        `json:"indexed_file_count"`
	PathOnlyFileCount     int        `json:"path_only_file_count"`
	SkippedFileCount      int        `json:"skipped_file_count"`
	HunkCount             int        `json:"hunk_count"`
	Additions             int        `json:"additions"`
	Deletions             int        `json:"deletions"`
	PatchBytes            int        `json:"patch_bytes"`
	BackfillInProgress    bool       `json:"backfill_in_progress"`
	InventoryNeedsRefresh bool       `json:"inventory_needs_refresh"`
	LastError             string     `json:"last_error,omitempty"`
}

type SearchMatch struct {
	PullRequestNumber int           `json:"pull_request_number"`
	State             string        `json:"state,omitempty"`
	Draft             bool          `json:"draft"`
	HeadSHA           string        `json:"head_sha,omitempty"`
	BaseRef           string        `json:"base_ref,omitempty"`
	IndexedAs         string        `json:"indexed_as,omitempty"`
	IndexFreshness    string        `json:"index_freshness,omitempty"`
	Score             float64       `json:"score"`
	SharedPaths       []string      `json:"shared_paths,omitempty"`
	OverlappingPaths  []string      `json:"overlapping_paths,omitempty"`
	OverlappingHunks  int           `json:"overlapping_hunks"`
	MatchedRanges     []MatchedPath `json:"matched_ranges,omitempty"`
	Reasons           []string      `json:"reasons,omitempty"`
}

type MatchedPath struct {
	Path     string `json:"path"`
	OldStart int    `json:"old_start,omitempty"`
	OldEnd   int    `json:"old_end,omitempty"`
	NewStart int    `json:"new_start,omitempty"`
	NewEnd   int    `json:"new_end,omitempty"`
}
