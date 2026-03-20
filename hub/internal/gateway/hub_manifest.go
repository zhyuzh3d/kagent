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
				Description:        "List accepted services and their current governance status.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.routes.get",
				Description:        "Inspect current tool routing bindings and candidates.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.routes.bind",
				Description:        "Update manual tool routing bindings in the Hub governance layer.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.audits.list",
				Description:        "Query recent Hub audit events.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.tool.probe",
				Description:        "Probe one routed tool through the Hub gateway.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.get",
				Description:        "Inspect one managed service including runtime and governance details.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.start",
				Description:        "Start one managed service instance.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.stop",
				Description:        "Stop one managed service instance.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.restart",
				Description:        "Restart one managed service instance.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.drain",
				Description:        "Drain one managed service instance.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.rebind",
				Description:        "Rebind routes after managed service changes.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.disable",
				Description:        "Temporarily disable one service from manual routing.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.manifest.get",
				Description:        "Read a managed service runtime manifest.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.manifest.update",
				Description:        "Update a managed service runtime manifest.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.config.get",
				Description:        "Read a managed service config file.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.config.update",
				Description:        "Update a managed service config file.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.files.list",
				Description:        "List editable files in one managed service workspace.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.files.read",
				Description:        "Read one file from a managed service workspace.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.files.write",
				Description:        "Write one file in a managed service workspace.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.build",
				Description:        "Build a managed service binary.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},
			{
				ToolID:             "hub.admin.service.generate",
				Description:        "Generate a custom managed service scaffold under the user workspace.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user"},
			},

			// Governance Tools
			{
				ToolID:             "hub.governance.service.register",
				Description:        "Register a service instance and its runtime tool view with the Hub.",
				Protocol:           "http",
				HubAuthRequired:    true,
				AllowedCallerTypes: []string{"service", "anonymous"}, // anonymous for bootstrap
			},
			{
				ToolID:             "hub.governance.service.heartbeat",
				Description:        "Refresh runtime heartbeat and health facts for a service instance.",
				Protocol:           "http",
				HubAuthRequired:    true,
				AllowedCallerTypes: []string{"service"},
			},
			{
				ToolID:             "hub.governance.service.drain",
				Description:        "Coordinate service drain before shutdown or route removal.",
				Protocol:           "http",
				HubAuthRequired:    true,
				AllowedCallerTypes: []string{"service", "user"},
			},

			// System Tools
			{
				ToolID:             "hub.system.version.get",
				Description:        "Return Hub version information.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"anonymous", "user", "service"},
			},
			{
				ToolID:             "hub.system.state.get",
				Description:        "Return Hub runtime governance state snapshot.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user", "service"},
			},
			{
				ToolID:             "hub.system.smoke.test",
				Description:        "Run Hub end-to-end smoke checks for core governance and auth flows.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user", "anonymous"}, // loopback restricted in handler
			},
			{
				ToolID:             "hub.system.report_log",
				Description:        "Report a structured log record into the Hub observability pipeline.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user", "service", "surface", "anonymous"},
			},
			{
				ToolID:             "hub.system.shutdown",
				Description:        "Gracefully stop the Hub process.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"user", "anonymous"}, // loopback restricted in handler
			},
			{
				ToolID:             "hub.system.health",
				Description:        "Return Hub health and runtime summary.",
				Protocol:           "http",
				AllowedCallerTypes: []string{"anonymous", "user", "service"},
			},
		},
	}
}
