package app

type ServiceToolDescriptor struct {
	ToolID               string         `json:"tool_id"`
	Category             string         `json:"category"`
	Type                 string         `json:"type"`
	Tool                 string         `json:"tool"`
	Description          string         `json:"description"`
	InputSchema          map[string]any `json:"input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	SideEffect           string         `json:"side_effect,omitempty"`
	CapabilitiesRequired []string       `json:"capabilities_required,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
	ScopeSupport         []string       `json:"scope_support,omitempty"`
}

type ServiceManifest struct {
	ServiceID           string                  `json:"service_id"`
	ServiceName         string                  `json:"service_name"`
	Version             string                  `json:"version,omitempty"`
	BuildHash           string                  `json:"build_hash,omitempty"`
	Reliability         string                  `json:"reliability,omitempty"`
	Visibility          string                  `json:"visibility,omitempty"`
	Entry               string                  `json:"entry,omitempty"`
	ConfigSchemaVersion int                     `json:"config_schema_version,omitempty"`
	Provides            []ServiceToolDescriptor `json:"provides,omitempty"`
	Requires            []string                `json:"requires,omitempty"`
}

func BuiltinServiceManifests() []ServiceManifest {
	return []ServiceManifest{
		{
			ServiceID:   "surface-manager",
			ServiceName: "Surface Manager",
			Version:     "1.0.0",
			Reliability: "verified",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "ui.surface.catalog_list", Category: "ui", Type: "surface", Tool: "catalog_list", Description: "list surface catalog"},
				{ToolID: "ui.surface.get", Category: "ui", Type: "surface", Tool: "get", Description: "get one surface"},
				{ToolID: "ui.surface.enable_set", Category: "ui", Type: "surface", Tool: "enable_set", Description: "set enabled"},
				{ToolID: "ui.surface.session_issue", Category: "ui", Type: "surface", Tool: "session_issue", Description: "issue session token"},
				{ToolID: "ui.surface.capability_issue", Category: "ui", Type: "surface", Tool: "capability_issue", Description: "issue capability token"},
				{ToolID: "ui.surface.runtime_status", Category: "ui", Type: "surface", Tool: "runtime_status", Description: "runtime status"},
				{ToolID: "ui.surface.logs_query", Category: "ui", Type: "surface", Tool: "logs_query", Description: "query surface logs"},
				{ToolID: "ui.surface.rescan", Category: "ui", Type: "surface", Tool: "rescan", Description: "rescan packages"},
				{ToolID: "ui.surface.rebind", Category: "ui", Type: "surface", Tool: "rebind", Description: "rebind manifest"},
				{ToolID: "ui.surface.fs_read", Category: "ui", Type: "surface", Tool: "fs_read", Description: "surface fs read"},
				{ToolID: "ui.surface.fs_write", Category: "ui", Type: "surface", Tool: "fs_write", Description: "surface fs write"},
				{ToolID: "ui.surface.fs_list", Category: "ui", Type: "surface", Tool: "fs_list", Description: "surface fs list"},
				{ToolID: "ui.surface.fs_delete", Category: "ui", Type: "surface", Tool: "fs_delete", Description: "surface fs delete"},
				{ToolID: "ui.surface.fs_sign_static", Category: "ui", Type: "surface", Tool: "fs_sign_static", Description: "surface fs sign static url"},
				{ToolID: "ui.surface.db_roundtrip", Category: "ui", Type: "surface", Tool: "db_roundtrip", Description: "service caller db roundtrip via hub"},
			},
		},
	}
}
