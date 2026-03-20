package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var allowedSurfacePackageWriteExt = map[string]struct{}{
	".html": {},
	".json": {},
	".js":   {},
	".css":  {},
	".md":   {},
	".txt":  {},
}

type SurfacePackageFile struct {
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	SizeBytes   int64  `json:"size_bytes"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func ResolveSurfacePackageDir(surfaceRoot string, entry SurfaceCatalogEntry) (string, error) {
	root := strings.TrimSpace(surfaceRoot)
	if root == "" {
		return "", fmt.Errorf("surface root is empty")
	}
	pkg := strings.Trim(strings.TrimSpace(entry.RawPkgPath), "/")
	if pkg == "" {
		return "", fmt.Errorf("surface package path is empty")
	}
	parts := []string{root}
	if strings.EqualFold(strings.TrimSpace(entry.SurfaceType), SurfaceTypeCustom) && strings.Contains(pkg, "/custom/") {
		parts = append(parts, strings.Split(pkg, "/")...)
	} else {
		parts = append(parts, strings.TrimSpace(entry.SurfaceType))
		parts = append(parts, strings.Split(pkg, "/")...)
	}
	target := filepath.Join(parts...)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("surface package escapes root")
	}
	return target, nil
}

func ReadSurfacePackageFile(surfaceRoot string, entry SurfaceCatalogEntry, relPath string) ([]byte, string, error) {
	path, err := ResolveSurfacePackageFile(surfaceRoot, entry, relPath)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	return raw, path, err
}

func WriteSurfacePackageFile(surfaceRoot string, entry SurfaceCatalogEntry, relPath string, data []byte) (string, error) {
	path, err := ResolveSurfacePackageFile(surfaceRoot, entry, relPath)
	if err != nil {
		return "", err
	}
	if _, ok := allowedSurfacePackageWriteExt[strings.ToLower(filepath.Ext(path))]; !ok {
		return "", fmt.Errorf("file type is not editable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

func ListSurfacePackageFiles(surfaceRoot string, entry SurfaceCatalogEntry) ([]SurfacePackageFile, string, error) {
	dir, err := ResolveSurfacePackageDir(surfaceRoot, entry)
	if err != nil {
		return nil, "", err
	}
	out := make([]SurfacePackageFile, 0, 8)
	err = filepath.WalkDir(dir, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, SurfacePackageFile{
			Path:        filepath.ToSlash(rel),
			IsDir:       d.IsDir(),
			SizeBytes:   info.Size(),
			UpdatedAtMS: info.ModTime().UnixMilli(),
		})
		return nil
	})
	return out, dir, err
}

func ResolveSurfacePackageFile(surfaceRoot string, entry SurfaceCatalogEntry, relPath string) (string, error) {
	dir, err := ResolveSurfacePackageDir(surfaceRoot, entry)
	if err != nil {
		return "", err
	}
	clean, err := normalizeRelativePath(relPath)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, clean)
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("surface package file escapes root")
	}
	return target, nil
}

func GenerateSurfaceScaffold(surfaceRoot string, userID string, surfaceName string, prompt string) (string, SurfaceManifest, error) {
	user := strings.TrimSpace(userID)
	slug := normalizePackageSlug(surfaceName)
	if user == "" || slug == "" {
		return "", SurfaceManifest{}, fmt.Errorf("user_id and surface_name are required")
	}
	manifest := SurfaceManifest{
		ID:                  newUUID(),
		Name:                slug,
		Version:             "1.0",
		MinSupportedVersion: "1.0",
		Entry:               "index.html",
		Desc:                firstNonEmpty(strings.TrimSpace(prompt), "generated surface"),
		Tags:                []string{"custom", "generated", user},
		Permissions: map[string]any{
			"sandbox": []string{"allow-scripts", "allow-downloads"},
			"allow":   []string{},
		},
	}
	dir := filepath.Join(strings.TrimSpace(surfaceRoot), user, SurfaceTypeCustom, slug)
	files := map[string]string{
		filepath.Join(dir, "manifest.json"): mustSurfaceJSON(manifest),
		filepath.Join(dir, "README.md"):     "# " + slug + "\n\n" + firstNonEmpty(strings.TrimSpace(prompt), "generated surface scaffold") + "\n",
		filepath.Join(dir, "index.html"):    renderGeneratedSurfaceHTML(manifest, prompt),
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", SurfaceManifest{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", SurfaceManifest{}, err
		}
	}
	return dir, manifest, nil
}

func ParseGeneratedFilesMap(raw string) (map[string]string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, fmt.Errorf("generated text is empty")
	}
	var payload struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err == nil && len(payload.Files) > 0 {
		return payload.Files, nil
	}
	if start := strings.Index(text, "{"); start >= 0 {
		text = text[start:]
		if err := json.Unmarshal([]byte(text), &payload); err == nil && len(payload.Files) > 0 {
			return payload.Files, nil
		}
	}
	return nil, fmt.Errorf("invalid generated files payload")
}

func renderGeneratedSurfaceHTML(manifest SurfaceManifest, prompt string) string {
	desc := firstNonEmpty(strings.TrimSpace(prompt), "generated surface scaffold")
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>%s</title>
  <style>
    body { margin: 0; font-family: "IBM Plex Sans", sans-serif; background: linear-gradient(160deg, #f5f7ec, #d7e5f0); color: #112; min-height: 100vh; display: grid; place-items: center; }
    .card { width: min(92vw, 520px); background: rgba(255,255,255,.92); border: 1px solid #cfd8dc; border-radius: 18px; padding: 18px; box-shadow: 0 12px 36px rgba(10,20,30,.14); }
    h1 { margin: 0 0 10px; font-size: 20px; }
    textarea { width: 100%%; min-height: 140px; border-radius: 12px; border: 1px solid #c7d2da; padding: 10px; font: inherit; }
    .row { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 12px; }
    button { border: 0; border-radius: 999px; padding: 8px 14px; background: #155e75; color: #fff; cursor: pointer; }
    pre { white-space: pre-wrap; background: #f8fafc; border: 1px solid #dde7ef; border-radius: 12px; padding: 10px; min-height: 60px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>%s</h1>
    <p>%s</p>
    <textarea id="noteInput">%s</textarea>
    <div class="row">
      <button id="syncBtn">同步状态</button>
      <button id="clearBtn">清空</button>
    </div>
    <pre id="stateBox">等待 Page 握手...</pre>
  </div>
  <script>
    let surfacePort = null;
    let surfaceID = "";
    let stateVersion = 0;
    const inputEl = document.getElementById("noteInput");
    const stateBox = document.getElementById("stateBox");
    function businessState() {
      return { note: inputEl.value || "", desc: %q };
    }
    function emitStateChange(eventType) {
      if (!surfacePort) return;
      stateVersion += 1;
      surfacePort.postMessage({
        type: "state_change",
        surface_id: surfaceID,
        surface_type: "custom",
        surface_version: "1.0",
        event_type: eventType || "state_change",
        business_state: businessState(),
        visible_text: inputEl.value || "",
        status: "ready",
        state_version: stateVersion,
        updated_at_ms: Date.now(),
      });
      stateBox.textContent = JSON.stringify(businessState(), null, 2);
    }
    function actionResult(action, status, result, errorText) {
      if (!surfacePort) return;
      surfacePort.postMessage({
        type: "action_result",
        action_id: action && action.id || "",
        action_name: action && action.name || "",
        status: status || "ok",
        result: result || {},
        error: errorText || "",
        surface_id: surfaceID,
        surface_type: "custom",
        surface_version: "1.0",
        business_state: businessState(),
        visible_text: inputEl.value || "",
        state_version: stateVersion,
        updated_at_ms: Date.now(),
      });
    }
    function handleAction(action) {
      const name = action && action.name ? action.name : "";
      const args = action && action.args && typeof action.args === "object" ? action.args : {};
      if (name === "get_state") {
        emitStateChange("get_state");
        actionResult(action, "ok", { state: businessState() }, "");
        return;
      }
      if (name === "set_note") {
        inputEl.value = typeof args.note === "string" ? args.note : inputEl.value;
        emitStateChange("set_note");
        actionResult(action, "ok", { note: inputEl.value }, "");
        return;
      }
      actionResult(action, "error", {}, "unsupported action");
    }
    document.getElementById("syncBtn").addEventListener("click", () => emitStateChange("sync"));
    document.getElementById("clearBtn").addEventListener("click", () => { inputEl.value = ""; emitStateChange("clear"); });
    inputEl.addEventListener("input", () => emitStateChange("input"));
    window.addEventListener("message", (event) => {
      const msg = event.data || {};
      if (msg.type !== "surface_connect") return;
      surfaceID = msg.surface_id || "";
      if (event.ports && event.ports[0]) {
        surfacePort = event.ports[0];
        surfacePort.onmessage = (ev) => {
          const data = ev.data || {};
          if (data.type === "surface_action") handleAction(data);
          if (data.type === "action_call" && data.action && typeof data.action === "object") handleAction(data.action);
        };
        surfacePort.start();
        surfacePort.postMessage({
          type: "surface_ready",
          surface_id: surfaceID,
          manifest: {
            surface_id: surfaceID,
            surface_type: "custom",
            surface_version: "1.0",
            title: %q,
            description: %q,
            actions: [
              { name: "get_state", description: "读取当前状态", args_schema: {} },
              { name: "set_note", description: "设置文本内容", args_schema: { note: "string" } }
            ]
          }
        });
        emitStateChange("ready");
      }
    });
  </script>
</body>
</html>
`, manifest.Name, manifest.Name, desc, desc, desc, manifest.Name, desc)
}

func normalizePackageSlug(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	text = strings.ReplaceAll(text, "-", "_")
	text = strings.ReplaceAll(text, " ", "_")
	for strings.Contains(text, "__") {
		text = strings.ReplaceAll(text, "__", "_")
	}
	text = strings.Trim(text, "_")
	if text == "" {
		return ""
	}
	return text
}

func mustSurfaceJSON(value any) string {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return string(raw) + "\n"
}

func newUUID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := base64.RawURLEncoding.EncodeToString(raw[:])
	hex := make([]byte, 32)
	for i := range raw {
		const digits = "0123456789abcdef"
		hex[i*2] = digits[raw[i]>>4]
		hex[i*2+1] = digits[raw[i]&0x0f]
	}
	_ = encoded
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])
}
