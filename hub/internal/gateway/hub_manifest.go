package gateway

import (
	"kagent/pkg/toolproto"
)

// HubManifest returns the manifest of the Hub itself as a virtual service.
func HubManifest() toolproto.SupervisorRegisterRequest {
	healthy := true
	return toolproto.SupervisorRegisterRequest{
		ServiceID:  "hub",
		InstanceID: "builtin-hub",
		Version:    "1.0.0",
		Transport:  "internal",
		Healthy:    &healthy,
		Tools: []toolproto.ServiceTool{
			// Admin Tools
			{
				ToolID:             "hub.admin.services.list",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.routes.get",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.routes.bind",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.audits.list",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.tool.probe",
				AllowedCallerTypes: []string{"user"},
			},

			// Governance Tools
			{
				ToolID:             "hub.governance.service.register",
				AllowedCallerTypes: []string{"service", "anonymous"}, // anonymous for bootstrap
			},
			{
				ToolID:             "hub.governance.service.heartbeat",
				AllowedCallerTypes: []string{"service"},
			},
			{
				ToolID:             "hub.governance.service.drain",
				AllowedCallerTypes: []string{"service", "user"},
			},

			// System Tools
			{
				ToolID:             "hub.system.version.get",
				AllowedCallerTypes: []string{"anonymous", "user", "service"},
			},
			{
				ToolID:             "hub.system.config.get",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.system.smoke.test",
				AllowedCallerTypes: []string{"user", "anonymous"}, // loopback restricted in handler
			},
			{
				ToolID:             "hub.system.report_log",
				AllowedCallerTypes: []string{"user", "service", "surface", "anonymous"},
			},
			{
				ToolID:             "hub.system.shutdown",
				AllowedCallerTypes: []string{"user", "anonymous"}, // loopback restricted in handler
			},
			{
				ToolID:             "hub.system.health",
				AllowedCallerTypes: []string{"anonymous", "user", "service"},
			},
		},
	}
}
