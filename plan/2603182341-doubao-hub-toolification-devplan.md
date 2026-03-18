# Development Plan: Full Toolification of Hub-Service Governance

## 1. Core Objective
Eliminate physical REST dependencies between Hub and Services. All vertical (governance) and horizontal (orchestration) communication must be unified under the tool gateway.

- **Service -> Hub**: Registration and Heartbeats via `hub.governance.*` tools.
- **Hub -> Service**: Service Shutdown and Health checks via `<service_id>.system.*` tools.

## 2. Implementation Phases

### Phase 1: AI-Doubao Service Refactoring (Governance Alignment)
1. **Refactor Registration Logic**: 
   - Wrap `SupervisorRegisterRequest` into `toolproto.CallRequest`.
   - Set `ToolID` to `hub.governance.service.register`.
2. **Refactor Heartbeat Logic**:
   - Wrap `SupervisorHeartbeatRequest` into `toolproto.CallRequest`.
   - Set `ToolID` to `hub.governance.service.heartbeat`.
3. **Expose System Tools**:
   - Register `ai.system.shutdown` and `ai.system.health` in the service manifest.
   - Implement handlers in `/service/tool/exec`.
4. **Endpoint Cleanup**:
   - Delete `/healthz`, `/service/info`, `/service/tools`, and `/admin/shutdown`.

### Phase 2: Hub Supervisor Orchestration (Tool-Based Control)
1. **LifecycleManager Enhancement**:
   - Inject `transport.Client` into `LifecycleManager`.
   - Update `stopProcess` to call `<service_id>.system.shutdown` using the transport client.
2. **Hub Initialization**:
   - Update `main.go` to provide the unified tool gateway URL to `LifecycleManager`.

### Phase 3: Manifest & Script Alignment
1. **Update Manifests**: Ensure `manifest.json` correctly reflects tool definitions.
2. **Clean Scripts**: Verify `deploy.sh` and others are compatible with the tool-only entry points.

## 3. Verification Criteria
- Hub logs show successful "Tool-based Registration".
- Hub can stop services via tool calls without 404s on legacy REST paths.
- Audit logs capture all governance and system control events as tool calls.
