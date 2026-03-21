package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type BuildResult struct {
	ServiceID string `json:"service_id"`
	Workdir   string `json:"workdir"`
	ExecPath  string `json:"exec_path"`
	TargetPkg string `json:"target_pkg"`
	Output    string `json:"output"`
}

func (m *LifecycleManager) BuildService(ctx context.Context, serviceID string) (BuildResult, error) {
	if m == nil {
		return BuildResult{}, fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return BuildResult{}, fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	buildArgs, workdir, targetPkg, err := m.resolveBuildCommand(svc)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(svc.execPath), 0o755); err != nil {
		return BuildResult{}, err
	}
	cmd := exec.CommandContext(ctx, buildArgs[0], buildArgs[1:]...)
	cmd.Dir = workdir
	raw, err := cmd.CombinedOutput()
	result := BuildResult{
		ServiceID: strings.TrimSpace(serviceID),
		Workdir:   workdir,
		ExecPath:  svc.execPath,
		TargetPkg: targetPkg,
		Output:    string(raw),
	}
	if err := os.MkdirAll(filepath.Dir(svc.startupManifest), 0o755); err != nil {
		return result, err
	}
	srcManifest := filepath.Join(svc.dirAbs, "manifest.json")
	if rawManifest, readErr := os.ReadFile(srcManifest); readErr == nil {
		_ = os.WriteFile(svc.startupManifest, rawManifest, 0o644)
	}
	_ = os.Remove(svc.runtimeManifest)

	srcConfig := filepath.Join(svc.dirAbs, "config")
	dstConfig := filepath.Join(svc.dirAbs, "run", "config")
	if fi, err := os.Stat(srcConfig); err == nil && fi.IsDir() {
		_ = os.MkdirAll(dstConfig, 0o755)
		_ = filepath.WalkDir(srcConfig, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(srcConfig, path)
			content, _ := os.ReadFile(path)
			_ = os.WriteFile(filepath.Join(dstConfig, rel), content, 0o644)
			return nil
		})
	}
	return result, nil
}

func (m *LifecycleManager) resolveBuildCommand(svc managedService) ([]string, string, string, error) {
	cmdDir := filepath.Join(svc.dirAbs, "cmd", strings.TrimSpace(svc.entry.ServiceID))
	if !isDir(cmdDir) {
		return nil, "", "", fmt.Errorf("service cmd dir not found: %s", cmdDir)
	}
	if isFile(filepath.Join(svc.dirAbs, "go.mod")) {
		pkg := "./cmd/" + strings.TrimSpace(svc.entry.ServiceID)
		return []string{"go", "build", "-buildvcs=false", "-o", svc.execPath, pkg}, svc.dirAbs, pkg, nil
	}
	rel, err := filepath.Rel(m.appRoot, cmdDir)
	if err != nil {
		return nil, "", "", err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return nil, "", "", fmt.Errorf("custom service outside app root requires its own go.mod")
	}
	pkg := "./" + rel
	return []string{"go", "build", "-buildvcs=false", "-o", svc.execPath, pkg}, m.appRoot, pkg, nil
}
