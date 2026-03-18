package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ResponseObserver wraps http.ResponseWriter to capture the HTTP status code.
type ResponseObserver struct {
	http.ResponseWriter
	Status int
}

// WriteHeader captures the status code before delegating.
func (o *ResponseObserver) WriteHeader(code int) {
	o.Status = code
	o.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker for WebSocket upgrade support.
func (o *ResponseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := o.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijack not supported by underlying ResponseWriter")
	}
	// WebSocket upgrade paths typically hijack the connection and may bypass WriteHeader.
	// Preserve accurate access-log status for successful upgrades.
	if o.Status == http.StatusOK {
		o.Status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

// Flush implements http.Flusher if the underlying ResponseWriter supports it.
func (o *ResponseObserver) Flush() {
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteJSON writes v as JSON with 200 OK status.
func WriteJSON(w http.ResponseWriter, v any) {
	WriteJSONStatus(w, http.StatusOK, v)
}

// WriteJSONStatus writes v as JSON with the given HTTP status code.
func WriteJSONStatus(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

// IsLoopbackRemoteAddr checks if the remote address is a loopback address.
func IsLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
