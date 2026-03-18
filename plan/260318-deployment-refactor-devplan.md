# Deployment & Service Governance Refactoring Plan

This plan outlines the refactoring of the Kagent deployment workflow to shift from a script-heavy injection model to a manifest-driven, Hub-centric governance model.

## User Review Required

> [!IMPORTANT]
> This refactoring will change the format of `services/*/manifest.json`. Existing manifests must be updated to include `entry` and `lifecycle` fields before the new `deploy.sh` is used.

## Proposed Changes

### 1. Service Manifest Evolution
Shift from dynamic injection to static, self-contained manifests.

#### [MODIFY] [all manifests](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/)
- **New Fields**: 
  - `entry`: Contains `args` and `env` for service startup.
  - `lifecycle`: Contains `register_timeout_ms`, `restart_policy`, etc.
- **Removed Fields**:
  - `reliability`: Moved to Hub global policy.
  - `provides`: Removed from static JSON; to be dynamically registered by Service code.

---

### 2. Hub-Centric Smoke Testing
Move validation logic from Bash to Go for better precision and environment consistency.

#### [NEW] [smoke_test.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/smoke_test.go)
- Implement a dedicated `SmokeTester` module.
- Logic includes:
  - User registration & login verification.
  - Tool routing health checks (ASR/LLM/Database).
- Triggered via a new internal API: `POST /api/system/smoke-test`.

---

### 3. Config-Driven Deployment Script
Refactor `deploy.sh` to be a generic process launcher.

#### [MODIFY] [deploy.sh](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh)
- **Logic**:
  1. Read `hub/config/config.json` to identify active services and their directories.
  2. Perform `go build` for each identified service.
  3. Copy `manifest.json` directly to the `run/` directory.
  4. Start services and Hub.
  5. Call Hub's smoke-test API to verify the deployment.

## Verification Plan

### Automated Tests
- `go test ./hub/internal/app/smoke_test_test.go` to verify the testing logic itself.
- Run `./scripts/deploy.sh` and verify it correctly identifies services from `config.json`.

### Manual Verification
- Check Hub logs for the new `Service:Report:SmokeTest` output.
- Verify that `run/manifest.json` in each service directory is a direct copy of the source manifest.
