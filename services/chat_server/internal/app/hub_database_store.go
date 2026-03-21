package app

import (
	"context"
	"strings"
	"sync"
)

type Project struct {
	ProjectID             string `json:"project_id"`
	UserID                string `json:"user_id"`
	Title                 string `json:"title"`
	CreatedAtMS           int64  `json:"created_at_ms"`
	LastActiveAtMS        int64  `json:"last_active_at_ms"`
	CreatedAtLocalWeekday string `json:"created_at_local_weekday"`
	CreatedAtLocalLunar   string `json:"created_at_local_lunar"`
	OrderIndex            int    `json:"order_index"`
}

type Thread struct {
	ThreadID              string `json:"thread_id"`
	UserID                string `json:"user_id"`
	ProjectID             string `json:"project_id"`
	Title                 string `json:"title"`
	CreatedAtMS           int64  `json:"created_at_ms"`
	LastActiveAtMS        int64  `json:"last_active_at_ms"`
	CreatedAtLocalWeekday string `json:"created_at_local_weekday"`
	CreatedAtLocalLunar   string `json:"created_at_local_lunar"`
	OrderIndex            int    `json:"order_index"`
}

type ChatStore interface {
	Close() error
	RuntimeUserID() string
	RuntimeProjectID() string
	RuntimeThreadID() string
	AppendMessage(msg ChatMessage) (ChatMessage, error)
	LoadSessionWindow(anchorLimit int, totalLimit int) ([]ChatMessage, error)
	LoadContextBeforeWithMode(beforeID int64, limit int, includeAllRoles bool) ([]ChatMessage, bool, error)
	ListProjectsForUser(userID string) ([]Project, error)
	CreateProject(userID string, title string) (string, error)
	UpdateProject(projectID string, title string, orderIndex int) error
	DeleteProject(projectID string) error
	ListThreadsForProject(userID string, projectID string) ([]Thread, error)
	CreateThread(userID string, projectID string, title string) (string, error)
	UpdateThread(threadID string, title string, orderIndex int, projectID string) error
	DeleteThread(threadID string) error
}

type HubDatabaseStore struct {
	client *HubToolClient

	mu        sync.Mutex
	userID    string
	projectID string
	threadID  string
	baseCtx   context.Context
	options   HubDatabaseStoreOptions
}

type HubDatabaseStoreOptions struct {
	EnsureDefaults bool
}

func NewHubDatabaseStore(ctx context.Context, client *HubToolClient, userID string, projectID string, threadID string) (*HubDatabaseStore, error) {
	return NewHubDatabaseStoreWithOptions(ctx, client, userID, projectID, threadID, HubDatabaseStoreOptions{EnsureDefaults: true})
}

func NewHubDatabaseStoreWithOptions(ctx context.Context, client *HubToolClient, userID string, projectID string, threadID string, options HubDatabaseStoreOptions) (*HubDatabaseStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s := &HubDatabaseStore{
		client:    client,
		userID:    strings.TrimSpace(userID),
		projectID: strings.TrimSpace(projectID),
		threadID:  strings.TrimSpace(threadID),
		baseCtx:   ctx,
		options:   options,
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *HubDatabaseStore) Close() error {
	return nil
}

func (s *HubDatabaseStore) RuntimeUserID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.userID)
}

func (s *HubDatabaseStore) RuntimeProjectID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.projectID)
}

func (s *HubDatabaseStore) RuntimeThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.threadID)
}
