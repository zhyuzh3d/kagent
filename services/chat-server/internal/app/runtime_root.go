package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DetectAppRoot() (string, error) {
	candidates := make([]string, 0, 6)
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
		add(filepath.Dir(filepath.Dir(exeDir)))
		add(filepath.Join(filepath.Dir(exeDir), "services", "chat-server"))
		add(filepath.Join(filepath.Dir(filepath.Dir(exeDir)), "services", "chat-server"))
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
		add(filepath.Join(cwd, "services", "chat-server"))
	}
	for _, c := range candidates {
		if isLikelyAppRoot(c) {
			return c, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0], fmt.Errorf("service root fallback in use, missing one of config/manifest.json")
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
	joined := filepath.Join(cleanRoot, cleanPath)
	if _, err := os.Stat(joined); err == nil {
		return joined
	}
	if _, err := os.Stat(cleanPath); err == nil {
		return cleanPath
	}
	return joined
}

func isLikelyAppRoot(path string) bool {
	configPath := filepath.Join(path, "config")
	manifestPath := filepath.Join(path, "manifest.json")
	if !isDir(configPath) || !isFile(manifestPath) {
		return false
	}
	return true
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}
