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
