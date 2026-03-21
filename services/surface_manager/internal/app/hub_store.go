package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
)

const (
	surfaceCatalogNamespace = "surface_manager"
	surfaceCatalogCategory  = "surface_catalog"
	surfaceDBName           = "surface_manager.db"
)

type SurfaceStore interface {
	Close() error
	EnsureSchema(context.Context) error
	SyncScannedSurfaces(context.Context, []ScannedSurface) error
	ListSurfacesForUser(context.Context, string) ([]SurfaceCatalogEntry, error)
	GetSurfaceForUser(context.Context, string, string) (SurfaceCatalogEntry, bool, error)
	SetSurfaceEnabled(context.Context, string, string, bool) error
	LoadRecentSurfaceMessages(context.Context, string, int) ([]ChatMessage, error)
}

type HubStore struct {
	toolCallURL string
	serviceAuth hubsvc.BootstrapSecret
	serviceID   string
	httpClient  *http.Client
}

func NewHubStore(toolCallURL string, serviceAuth hubsvc.BootstrapSecret, serviceID string, timeout time.Duration) *HubStore {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &HubStore{
		toolCallURL: strings.TrimSpace(toolCallURL),
		serviceAuth: serviceAuth,
		serviceID:   strings.TrimSpace(serviceID),
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func (s *HubStore) Close() error {
	return nil
}
