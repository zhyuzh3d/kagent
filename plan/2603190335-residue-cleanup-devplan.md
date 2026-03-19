# Dev Plan: Obsolete Residue Cleanup (bin/ & logs/)

## 1. Context
Comprehensive check revealed that `bin/` and `logs/` directories are no longer useful in the current architecture.
- `bin/`: Historical binaries and symlinks.
- `logs/`: Deprecated log sink replaced by unified `log.txt`.
- `run/`: Remains essential for PID and service runtime artifacts.

## 2. Tasks

### Phase 1: File Cleanup
- [ ] Physically delete `bin/` directory and its contents.
- [ ] Physically delete `logs/` directory and its contents.

### Phase 2: Script Refactoring
- [ ] Update `scripts/deploy.sh`:
    - Remove `logs` from `mkdir -p logs run data`.
    - (Optional) Double check if any other part of the script refers to `logs/`.

### Phase 3: Documentation Sync
- [ ] Update `doc/_instruction/structure.md`:
    - Remove `bin/` and `logs/` from the directory tree or mark them as "Deprecated/Removed".
    - Update descriptions if necessary.
- [ ] Update `doc/_devlog.md`:
    - Record the cleanup action with absolute timestamp.

## 3. Verification
- [ ] Run `ls -d bin/ logs/` to ensure they are gone.
- [ ] Run `grep -r "logs/" scripts/` to ensure no active script depends on it.
- [ ] Run `./scripts/deploy.sh` (or `go build ./hub/...`) to ensure no build regression.

## 4. Rollback
- Since these are deletions, manual restore from Git (if they were tracked) or just recreation (if they were empty) would be needed. However, they were already in `.gitignore`.
