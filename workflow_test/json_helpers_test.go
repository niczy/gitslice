package workflow

type changesetCreateJSON struct {
	ChangesetID string `json:"changeset_id"`
	Status      string `json:"status"`
}

type mergeJSON struct {
	Status string `json:"status"`
}

type sliceCreateJSON struct {
	SliceID string `json:"slice_id"`
	Slug    string `json:"slug"`
}

type sliceListJSON struct {
	Total  int `json:"total"`
	Slices []struct {
		SliceID string `json:"slice_id"`
		Slug    string `json:"slug"`
	} `json:"slices"`
}

type sliceStatusJSON struct {
	SliceID            string `json:"slice_id"`
	Mode               string `json:"mode"`
	WorkingTree        string `json:"working_tree"`
	SyncStatus         string `json:"sync_status"`
	PathCount          int    `json:"path_count"`
	TrackedChangesetID string `json:"tracked_changeset_id"`
	RemoteQueried      bool   `json:"remote_queried"`
	RemoteHead         string `json:"remote_head"`
	Changes            struct {
		Added    int `json:"added"`
		Modified int `json:"modified"`
		Deleted  int `json:"deleted"`
	} `json:"changes"`
}

type changesetReviewJSON struct {
	ChangesetID  string `json:"changeset_id"`
	ReviewStatus string `json:"review_status"`
	Diff         struct {
		FilesAdded    int32 `json:"files_added"`
		FilesModified int32 `json:"files_modified"`
		FilesDeleted  int32 `json:"files_deleted"`
	} `json:"diff"`
}

type slicePublishJSON struct {
	Changeset struct {
		ChangesetID string `json:"changeset_id"`
		Status      string `json:"status"`
	} `json:"changeset"`
	Review struct {
		ChangesetID  string `json:"changeset_id"`
		ReviewStatus string `json:"review_status"`
		Diff         struct {
			FilesAdded    int32 `json:"files_added"`
			FilesModified int32 `json:"files_modified"`
			FilesDeleted  int32 `json:"files_deleted"`
		} `json:"diff"`
	} `json:"review"`
	ReviewOnly bool `json:"review_only"`
	Merge      *struct {
		Status string `json:"status"`
	} `json:"merge"`
}

type changesetListJSON struct {
	Total      int `json:"total"`
	Changesets []struct {
		ChangesetID string `json:"changeset_id"`
		Status      string `json:"status"`
	} `json:"changesets"`
}

type sliceDeleteJSON struct {
	SliceID string `json:"slice_id"`
	Status  string `json:"status"`
}
