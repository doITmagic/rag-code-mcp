package html

// CodeChunk represents a section or meaningful part of an HTML document.
type CodeChunk struct {
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Language  string         `json:"language"`
	FilePath  string         `json:"file_path"`
	Signature string         `json:"signature,omitempty"`
	Docstring string         `json:"docstring,omitempty"`
	Content   string         `json:"code"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
