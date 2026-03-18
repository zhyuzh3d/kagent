# Enhancement of `scripts/deploy.sh` Log Output

Currently, `deploy.sh` builds and starts Hub in the background, then exits. Script logs and Hub logs are separate (one to terminal, one to `log.txt`). The user wants the script logs to be included in `log.txt` and the script to end by showing constant log output.

## Proposed Changes

### [scripts/deploy.sh](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh)

1.  **Uniform Redirection**: After rotation (around line 78), we'll use `exec > >(tee -a "${hub_log}") 2>&1` to capture all subsequent stdout/stderr into `log.txt` while still showing it in the terminal.
2.  **Interactive Log Monitoring**: At the very end of the script (after line 84), we'll add `tail -F "${hub_log}"`. This will keep the terminal active and showing logs until the user manually interrupts it (Ctrl+C).
3.  **Adjusting Messages**: Update the final success message to reflect that the script is now in monitoring mode.

## Verification Plan

### Automated Tests
-   Check script syntax with `bash -n scripts/deploy.sh`.
-   Run the script (simulate a tiny bit if possible, or just review carefully). Since it starts a server, I'll check if the redirection works.

### Manual Verification
-   Run `./scripts/deploy.sh` and confirm:
    -   Script logs appear in both terminal and `log.txt`.
    -   Script does NOT terminate but waits for logs (`tail -f`).
