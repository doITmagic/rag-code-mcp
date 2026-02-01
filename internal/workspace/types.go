package workspace

import "time"

// Info contains information about a detected workspace
type Info struct {
	// Root is the absolute path to the workspace root directory
	Root string `json:"root"`

	// ID is a stable, unique identifier for this workspace (hash of Root)
	ID string `json:"id"`

	// ProjectType indicates the detected project type (go, node, python, etc.)
	ProjectType string `json:"project_type,omitempty"`

	// Languages is the list of programming languages detected in this workspace
	// For polyglot workspaces (e.g., Go + Python microservices)
	Languages []string `json:"languages,omitempty"`

	// Markers are the workspace markers found (e.g., ".git", "go.mod")
	Markers []string `json:"markers,omitempty"`

	// DetectedAt is when this workspace was first detected
	DetectedAt time.Time `json:"detected_at,omitempty"`

	// CollectionPrefix is the prefix used for this workspace's collection
	// Set by Manager based on config
	CollectionPrefix string `json:"collection_prefix,omitempty"`
}

// CollectionName returns the Qdrant collection name for this workspace
// Deprecated: Use CollectionNameForLanguage instead for multi-language support
func (w *Info) CollectionName() string {
	prefix := w.CollectionPrefix
	if prefix == "" {
		prefix = "ragcode" // Default prefix
	}
	return prefix + "-" + w.ID
}

// CollectionNameForLanguage returns the Qdrant collection name for a specific language in this workspace
// Format: {prefix}-{workspaceID}-{language}
// Example: ragcode-a1b2c3d4e5f6-go, ragcode-a1b2c3d4e5f6-python
func (w *Info) CollectionNameForLanguage(language string) string {
	prefix := w.CollectionPrefix
	if prefix == "" {
		prefix = "ragcode" // Default prefix
	}
	if language == "" {
		// Fallback to old behavior if no language specified
		return prefix + "-" + w.ID
	}
	return prefix + "-" + w.ID + "-" + language
}

// Metadata represents workspace metadata stored in Qdrant
type Metadata struct {
	WorkspaceID  string    `json:"workspace_id"`
	RootPath     string    `json:"root_path"`
	Language     string    `json:"language"` // Programming language (go, python, php, etc.)
	LastIndexed  time.Time `json:"last_indexed"`
	FileCount    int       `json:"file_count"`
	ChunkCount   int       `json:"chunk_count"`
	Status       string    `json:"status"` // "indexed", "indexing", "failed"
	ProjectType  string    `json:"project_type,omitempty"`
	Markers      []string  `json:"markers,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// IndexingStatus represents possible indexing states
const (
	StatusIndexed  = "indexed"
	StatusIndexing = "indexing"
	StatusFailed   = "failed"
	StatusPending  = "pending"
)
