# Hub-Centric Orchestration & Minimalist Deployment Plan

This plan refactors the Kagent system to move orchestration and governance logic entirely into the Hub, leaving the deployment script as a simple builder and process starter.

## User Review Required

> [!IMPORTANT]
> - `deploy.sh` will no longer verify service health or run smoke tests; it will delegate this to the Hub.
> - The Hub will now have write access to service `run/` directories to inject `.service_secret`.
> - A 3-second timeout for graceful shutdown will be enforced by `deploy.sh` before force-killing the Hub.

## Proposed Changes

### 1. Hub Orchestration (Internal)
Move governance and initialization logic from scripts to Go code.

#### [MODIFY] [lifecycle.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/lifecycle.go)
- **Secret Generation**: In `startService`, generate a random `S2H_TOKEN` and `H2S_TOKEN` and write them to `run/.service_secret` before calling `exec.Command`.
- **Cleanup**: Ensure `.service_secret` is created with `0600` permissions.

#### [MODIFY] [main.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go)
- **Automatic Smoke Test**: Update the `lifecycleManager.StartAll` call block. After all services are successfully started, immediately instantiate `app.SmokeTester` and call `Run()`.
- **Log Convergence**: Log the results of the automated smoke test to the Hub's main log.

---

### 2. Minimalist Deployment Script
Refactor `deploy.sh` to focus only on building and the Hub's process lifecycle.

#### [MODIFY] [deploy.sh](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh)
- **Build**: Continue to build Hub and all services defined in `config.json`.
- **Sync**: Only sync `manifest.json`. **Remove** any smoke test calls or binary health checks.
- **Stop Logic**:
  1. Attempt graceful shutdown via `/admin/shutdown`.
  2. Wait for **3 seconds**.
  3. If PID is still alive, send `SIGKILL`.
- **Final Step**: Just start the Hub with `nohup` and tail the logs.

---

### 3. Service-Side Bootstrap
Update services to read the Hub-injected secret.

#### [MODIFY] [hubsvc/session.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/hubsvc/session.go) (or similar)
- Enhance the bootstrap logic to read `run/.service_secret` and use the tokens for registration and incoming request verification.

## Verification Plan

### Automated Tests
- `go test ./hub/internal/supervisor/...` to verify the new `LifecycleManager` sequence.
- Run `scripts/deploy.sh` and verify:
    - Services are built.
    - Hub starts.
    - Hub logs show "Automated Smoke Test Started" and subsequent "Success".
    - `run/.service_secret` files are generated and contain the expected tokens.

### Manual Verification
- Manually kill a service and verify the Hub regenerates the secret and restarts it (if policy allows).
- Verify `deploy.sh` correctly force-kills the Hub if it hangs during shutdown (can be tested by mocking a slow shutdown in `main.go`).
