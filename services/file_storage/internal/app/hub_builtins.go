package app

func BuiltinServiceManifests() []ServiceManifest {
	return []ServiceManifest{
		{
			ServiceID:   "file_storage",
			ServiceName: "file_storage",
			Version:     "1.0.0",
			Reliability: "trusted",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "service.lifecycle.health", Category: "service", Type: "lifecycle", Tool: "health", Description: "service health probe", AllowedCallerTypes: []string{"service"}},
				{ToolID: "service.lifecycle.state.get", Category: "service", Type: "lifecycle", Tool: "state.get", Description: "service lifecycle state snapshot", AllowedCallerTypes: []string{"service"}},
				{ToolID: "service.lifecycle.shutdown", Category: "service", Type: "lifecycle", Tool: "shutdown", Description: "service shutdown", AllowedCallerTypes: []string{"service"}},
				{ToolID: "storage.file.read", Category: "storage", Type: "file", Tool: "read", Description: "read file", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.write", Category: "storage", Type: "file", Tool: "write", Description: "write file", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.delete", Category: "storage", Type: "file", Tool: "delete", Description: "delete file", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.exists", Category: "storage", Type: "file", Tool: "exists", Description: "check file existence", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.stat", Category: "storage", Type: "file", Tool: "stat", Description: "file metadata", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.list", Category: "storage", Type: "file", Tool: "list", Description: "list dir entries", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.mkdir", Category: "storage", Type: "file", Tool: "mkdir", Description: "create dir", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.rename", Category: "storage", Type: "file", Tool: "rename", Description: "rename path", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.copy", Category: "storage", Type: "file", Tool: "copy", Description: "copy file", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
				{ToolID: "storage.blob.put", Category: "storage", Type: "blob", Tool: "put", Description: "put immutable blob", ScopeSupport: []string{"user"}, AllowedCallerTypes: []string{"user"}},
				{ToolID: "storage.blob.get", Category: "storage", Type: "blob", Tool: "get", Description: "get immutable blob", ScopeSupport: []string{"user"}, AllowedCallerTypes: []string{"user"}},
				{ToolID: "storage.blob.sign_url", Category: "storage", Type: "blob", Tool: "sign_url", Description: "sign blob download url", ScopeSupport: []string{"user"}, AllowedCallerTypes: []string{"user"}},
				{ToolID: "storage.blob.gc", Category: "storage", Type: "blob", Tool: "gc", Description: "gc expired blobs", ScopeSupport: []string{"service"}, AllowedCallerTypes: []string{"service"}},
			},
		},
	}
}
