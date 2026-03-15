package models

// Block represents a content-addressed chunk of file data.
type Block struct {
	Hash string `json:"hash"`
	Size int    `json:"size"`
}

// FileManifest describes a file as an ordered list of blocks.
type FileManifest struct {
	Path          string  `json:"path"`
	TotalSize     int64   `json:"total_size"`
	Hash          string  `json:"hash"`
	Blocks        []Block `json:"blocks"`
	Executable    bool    `json:"executable,omitempty"`
	SymlinkTarget string  `json:"symlink_target,omitempty"`
}
