package core

import "encoding/json"

// WebMCPToolDecl is the decoded form of one entry of the webmcp_tools list that a plugin manifest
// (plugin.json) or a theme (theme.json) may declare (ticket 13). The server
// validates every declaration before it reaches GET /_jelly/webmcp-tools.json:
// the name must match ^[a-z][a-z0-9_]{0,63}$, the endpoint must be a same-origin
// GET path, readOnlyHint is always published as true and consequentialHint is
// never published. Invalid or duplicate declarations are skipped with a warning.
type WebMCPToolDecl struct {
	Name        string            `json:"name"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description"`
	InputSchema json.RawMessage   `json:"input_schema,omitempty"`
	Endpoint    string            `json:"endpoint"`
	Annotations WebMCPAnnotations `json:"annotations,omitempty"`
}

// WebMCPAnnotations mirrors the ToolAnnotations dictionary of the WebMCP draft
// (readOnlyHint, untrustedContentHint, consequentialHint, debugging). The
// declared readOnlyHint and consequentialHint are ignored on output.
type WebMCPAnnotations struct {
	ReadOnlyHint         bool `json:"readOnlyHint,omitempty"`
	UntrustedContentHint bool `json:"untrustedContentHint,omitempty"`
	ConsequentialHint    bool `json:"consequentialHint,omitempty"`
	Debugging            bool `json:"debugging,omitempty"`
}
