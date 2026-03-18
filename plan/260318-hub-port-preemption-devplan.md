# Hub Port Preemption (Self-Cleaning Startup)

Implement a mechanism in the Hub to automatically detect and clear the port it intends to occupy (default 18080). This ensures that a new Hub instance can always start successfully, even if a previous instance didn't shut down correctly or another process is using the port.

## Proposed Changes

### [Component: Hub]

#### [NEW] [port_preempt.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/port_preempt.go)
- Implement `EnsurePortReady(addr string)` utility.
- **Workflow**:
  1. Try to listen on the address.
  2. If failed (port in use), send a POST request to `http://<addr>/admin/shutdown`.
  3. Wait for 800ms.
  4. Try to listen again.
  5. If still failed, use `lsof` (on Mac/Linux) to find the PID and execute `kill -9`.
  6. Log a single concise line: `System:Internal:Startup:PortPreempted: <port>`.

#### [MODIFY] [main.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go)
- Call `app.EnsurePortReady(*addr)` at the very beginning of the `main` function (after `flag.Parse` and `InitLogger`).

## Verification Plan

### Manual Verification
1. **Prepare Binary**:
   ```bash
   go build -o kagent_test ./hub/cmd/hub
   ```
2. **Standard Graceful Flow**:
   - Terminal A: Run `./kagent_test -addr 127.0.0.1:18080`
   - Terminal B: Run `./kagent_test -addr 127.0.0.1:18080`
   - **Expectation**: Terminal A should exit gracefully. Terminal B should log `System:Internal:Startup:PortPreempted: 18080` and start successfully.
3. **Force Kill Flow**:
   - Terminal A: Run `nc -l 18080` (to simulate a non-Hub process or a hung process).
   - Terminal B: Run `./kagent_test -addr 127.0.0.1:18080`
   - **Expectation**: Terminal B should detect occupancy, fail to shut down via HTTP, then find the `nc` PID and kill it. Terminal B should then start successfully.
