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
				{ToolID: "storage.file.read", Category: "storage", Type: "file", Tool: "read", Description: "读取指定路径的文件内容", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "相对路径"}, "encoding": map[string]any{"type": "string", "default": "utf-8"}}, "required": []string{"path"}}},
				{ToolID: "storage.file.write", Category: "storage", Type: "file", Tool: "write", Description: "写入内容到指定路径的文件 (覆盖式)", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}},
				{ToolID: "storage.file.delete", Category: "storage", Type: "file", Tool: "delete", Description: "删除指定路径的文件或目录", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
				{ToolID: "storage.file.exists", Category: "storage", Type: "file", Tool: "exists", Description: "检查文件或目录是否存在", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
				{ToolID: "storage.file.stat", Category: "storage", Type: "file", Tool: "stat", Description: "获取文件的元数据信息 (大小、修改时间等)", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
				{ToolID: "storage.file.list", Category: "storage", Type: "file", Tool: "list", Description: "列出目录下的文件和子目录列表", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "目录相对路径"}}, "required": []string{"path"}}},
				{ToolID: "storage.file.mkdir", Category: "storage", Type: "file", Tool: "mkdir", Description: "创建目录，支持递归创建", ScopeSupport: []string{"user", "surface", "service"}, AllowedCallerTypes: []string{"user", "surface", "service"}},
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
