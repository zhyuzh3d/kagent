package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	app "kagent/hub/internal/app"
)

// NewLoggingMiddleware returns an HTTP middleware that logs System:HTTP: audit entries.
// Requests whose path matches any of silentPrefixes are served without logging.
func NewLoggingMiddleware(handler http.Handler, silentPrefixes []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Selective silence for high-frequency or internal setup noise
		for _, prefix := range silentPrefixes {
			if strings.HasPrefix(path, prefix) || strings.HasSuffix(path, prefix) {
				handler.ServeHTTP(w, r)
				return
			}
		}

		start := time.Now()
		observer := &app.ResponseObserver{ResponseWriter: w, Status: http.StatusOK}

		extra := ""
		if path == "/api/tool/call" && r.Method == http.MethodPost {
			// Peek at body to extract tool_id
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var payload struct {
				ToolID string `json:"tool_id"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.ToolID != "" {
				extra = fmt.Sprintf(" [%s]", payload.ToolID)
			}
		}

		handler.ServeHTTP(observer, r)

		// Audit logs for all incoming requests - dynamically tagged based on identity
		identity := app.IdentityFromContext(r.Context())
		tag := "HUB"
		switch identity.Type {
		case app.IdentityService:
			tag = strings.ToUpper(identity.Name)
		case app.IdentityUser:
			tag = "PAGE"
		case app.IdentitySurface:
			tag = "SURF"
		}

		target := path
		if extra != "" {
			target = strings.Trim(extra, " []")
		} else {
			target = fmt.Sprintf("%s %s", r.Method, path)
		}

		// Category 2: System:HTTP:
		// Format: System:HTTP:<Method> <Path> [<Status>] (<Duration>)
		msg := fmt.Sprintf("System:HTTP:%s [%d] (%v)", target, observer.Status, time.Since(start))
		app.InfofCtxTag(r.Context(), tag, "%s", msg)
	})
}
