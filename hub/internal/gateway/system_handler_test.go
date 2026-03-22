package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStaticPathNoRedirectServesIndexHTMLWithoutRedirect(t *testing.T) {
	root := t.TempDir()
	surfaceDir := filepath.Join(root, "surface", "buildin", "counter")
	if err := os.MkdirAll(surfaceDir, 0o755); err != nil {
		t.Fatalf("mkdir surface dir: %v", err)
	}
	indexPath := filepath.Join(surfaceDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<!doctype html><title>counter</title>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	resolvedPath, info, err := resolveStaticPathNoRedirect(root, "/surface/buildin/counter/index.html")
	if err != nil {
		t.Fatalf("resolveStaticPathNoRedirect returned error: %v", err)
	}
	if resolvedPath != indexPath {
		t.Fatalf("resolved path mismatch: got %s want %s", resolvedPath, indexPath)
	}
	if info == nil || info.IsDir() {
		t.Fatalf("expected regular file info, got %#v", info)
	}

	dirResolvedPath, dirInfo, err := resolveStaticPathNoRedirect(root, "/surface/buildin/counter/")
	if err != nil {
		t.Fatalf("resolveStaticPathNoRedirect directory request returned error: %v", err)
	}
	if dirResolvedPath != indexPath {
		t.Fatalf("directory resolve mismatch: got %s want %s", dirResolvedPath, indexPath)
	}
	if dirInfo == nil || dirInfo.IsDir() {
		t.Fatalf("expected directory request to resolve to file info, got %#v", dirInfo)
	}
}

func TestHandleStaticFilesSurfaceDisablesCacheAndRedirect(t *testing.T) {
	root := t.TempDir()
	surfaceDir := filepath.Join(root, "surface", "buildin", "counter")
	if err := os.MkdirAll(surfaceDir, 0o755); err != nil {
		t.Fatalf("mkdir surface dir: %v", err)
	}
	indexBody := "<!doctype html><title>counter</title>"
	if err := os.WriteFile(filepath.Join(surfaceDir, "index.html"), []byte(indexBody), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	handler := &SystemHandler{webuiRoot: root}
	req := httptest.NewRequest(http.MethodGet, "/surface/buildin/counter/index.html", nil)
	rec := httptest.NewRecorder()

	handler.HandleStaticFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	if location := rec.Header().Get("Location"); location != "" {
		t.Fatalf("expected no redirect location header, got %q", location)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected Cache-Control: %q", got)
	}
	if got := rec.Body.String(); got != indexBody {
		t.Fatalf("unexpected body: got %q want %q", got, indexBody)
	}
}

func TestHandleStaticFilesSurfaceRewritesHTMLAssetsWithReloadToken(t *testing.T) {
	root := t.TempDir()
	surfaceDir := filepath.Join(root, "surface", "buildin", "gomoku")
	if err := os.MkdirAll(surfaceDir, 0o755); err != nil {
		t.Fatalf("mkdir surface dir: %v", err)
	}
	htmlBody := `<!doctype html><link rel="stylesheet" href="./style.css"><script type="module" src="./app.js"></script>`
	if err := os.WriteFile(filepath.Join(surfaceDir, "index.html"), []byte(htmlBody), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	handler := &SystemHandler{webuiRoot: root}
	req := httptest.NewRequest(http.MethodGet, "/surface/buildin/gomoku/?_surface_reload=abc123", nil)
	rec := httptest.NewRecorder()

	handler.HandleStaticFiles(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `./style.css?_surface_reload=abc123`) {
		t.Fatalf("expected rewritten stylesheet url, got %q", body)
	}
	if !strings.Contains(body, `./app.js?_surface_reload=abc123`) {
		t.Fatalf("expected rewritten script url, got %q", body)
	}
}

func TestRewriteJSImportSpecifiersAppendsReloadToken(t *testing.T) {
	raw := strings.Join([]string{
		`import "./boot.js";`,
		`import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";`,
		`const mod = import("./lazy.js");`,
		`export { foo } from "./shared.js";`,
	}, "\n")

	rewritten := rewriteJSImportSpecifiers(raw, "abc123")

	for _, expected := range []string{
		`import "./boot.js?_surface_reload=abc123";`,
		`from "../../../lib/surfaceTool.js?_surface_reload=abc123";`,
		`import("./lazy.js?_surface_reload=abc123")`,
		`from "./shared.js?_surface_reload=abc123";`,
	} {
		if !strings.Contains(rewritten, expected) {
			t.Fatalf("expected %q in rewritten js, got %q", expected, rewritten)
		}
	}
}
