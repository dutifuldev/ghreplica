package gitindex

import "time"

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
