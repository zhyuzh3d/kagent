package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DetectAppRoot() (string, error) {
	candidates := make([]string, 0, 4)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		for _, existing := range candidates {
			if existing == abs {
				return
			}
		}
		candidates = append(candidates, abs)
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		add(exeDir)
		add(filepath.Dir(exeDir))
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
	}
	for _, c := range candidates {
		if isLikelyAppRoot(c) {
			return c, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0], fmt.Errorf("app root fallback in use, missing one of webui/config")
	}
	return ".", fmt.Errorf("unable to detect app root")
}

func ResolvePathFromRoot(root string, rawPath string) string {
	cleanRoot := strings.TrimSpace(root)
	cleanPath := strings.TrimSpace(rawPath)
	if cleanPath == "" {
		return cleanPath
	}
	if filepath.IsAbs(cleanPath) {
		return cleanPath
	}
	if cleanRoot == "" {
		return cleanPath
	}
	return filepath.Join(cleanRoot, cleanPath)
}

func isLikelyAppRoot(path string) bool {
	webuiPath := filepath.Join(path, "webui")
	configPath := filepath.Join(path, "config")
	if !isDir(webuiPath) || !isDir(configPath) {
		return false
	}
	return true
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}
