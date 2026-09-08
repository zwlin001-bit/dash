package cloud

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"dash/internal/app"
	"dash/internal/credentials"
	"dash/internal/crypto"
	"dash/internal/logx"
	"dash/internal/provider"
)

// CredentialStore defines the credential storage operations.
type CredentialStore interface {
	List(ctx context.Context) ([]credentials.Summary, error)
	Create(ctx context.Context, name, credKind string, payload []byte) (*credentials.Summary, error)
	Delete(ctx context.Context, id string) error
}

// CloudService defines the cloud resource operations.
type CloudService interface {
	EnsureBuiltinProviders(ctx context.Context) error
	ListAccounts(ctx context.Context) ([]CloudAccount, error)
	GetAccount(ctx context.Context, id string) (*CloudAccount, error)
	CreateAccount(ctx context.Context, acc CloudAccount) (*CloudAccount, error)
	UpdateAccount(ctx context.Context, id string, acc CloudAccount) (*CloudAccount, error)
	DeleteAccount(ctx context.Context, id string) error
	DiscoverAccount(ctx context.Context, credID string, regions []string, site string) ([]provider.NormalizedResource, error)
	ListRegions(ctx context.Context, credID string) ([]provider.Region, error)
	TriggerSync(ctx context.Context, accountID string) (string, error)
	GetSyncStatus(jobID string) (*SyncJobStatus, error)
	ListResources(ctx context.Context, accountID, providerCode, resKind, region, status string) ([]CloudResource, error)
	GetResource(ctx context.Context, id string) (*CloudResource, error)
	ActionResource(ctx context.Context, id, action string) (*provider.ActionResponse, error)
}

// Module implements app.Module for cloud resources and credentials.
type Module struct {
	svc       CloudService
	credStore CredentialStore
	pm        *provider.Manager
}

func NewModule() *Module {
	return &Module{}
}

// NewModuleWithServices creates a Module with injected services (for testing and fixture generation).
func NewModuleWithServices(credStore CredentialStore, svc CloudService) *Module {
	m := &Module{
		credStore: credStore,
		svc:       svc,
	}
	return m
}

// RegisterRoutes registers cloud routes directly to an App (useful when services are injected).
func (m *Module) RegisterRoutes(a *app.App) {
	m.registerRoutes(a)
}

func (m *Module) Name() string {
	return "cloud"
}

func (m *Module) Register(a *app.App) error {
	masterKeyPath := "/etc/dash/master.key"
	if a.Config != nil && a.Config.Server.MasterKey != "" {
		masterKeyPath = a.Config.Server.MasterKey
	}

	masterKey, err := crypto.LoadMasterKey(masterKeyPath)
	if err != nil {
		// In tests or dev mode, fallback to in-memory random 32-byte master key
		if flag.Lookup("test.v") != nil || (a.Config != nil && a.Config.Server.DevNoAuth) || os.Getenv("DASH_ENV") == "dev" {
			masterKey = make([]byte, 32)
			_, _ = rand.Read(masterKey)
		} else {
			// In production, master key missing must return clear error
			return fmt.Errorf("cloud module: failed to load master key: %w", err)
		}
	}

	rawCredStore := credentials.NewStore(a.DB, masterKey)
	m.credStore = rawCredStore
	m.pm = provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")
	m.svc = NewService(a.DB, rawCredStore, m.pm)

	if a.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.svc.EnsureBuiltinProviders(ctx); err != nil {
			logx.Warn("ensure builtin providers failed", "err", err)
		}
	}

	a.CloudService = m.svc
	m.registerRoutes(a)
	return nil
}

func (m *Module) Service() CloudService {
	return m.svc
}

func (m *Module) CredStore() CredentialStore {
	return m.credStore
}

func (m *Module) ProviderManager() *provider.Manager {
	return m.pm
}

func (m *Module) registerRoutes(a *app.App) {
	// Credentials
	a.HandleAuthed("GET /api/v1/credentials", m.handleListCredentials)
	a.HandleAuthed("POST /api/v1/credentials", m.handleCreateCredential)
	a.HandleAuthed("DELETE /api/v1/credentials/{id}", m.handleDeleteCredential)

	// Cloud Accounts
	a.HandleAuthed("GET /api/v1/cloud-accounts", m.handleListAccounts)
	a.HandleAuthed("POST /api/v1/cloud-accounts", m.handleCreateAccount)
	a.HandleAuthed("GET /api/v1/cloud-accounts/{id}", m.handleGetAccount)
	a.HandleAuthed("PUT /api/v1/cloud-accounts/{id}", m.handleUpdateAccount)
	a.HandleAuthed("DELETE /api/v1/cloud-accounts/{id}", m.handleDeleteAccount)

	// Auto-discovery & Sync
	a.HandleAuthed("GET /api/v1/cloud-accounts/regions", m.handleListRegions)
	a.HandleAuthed("POST /api/v1/cloud-accounts/discover", m.handleDiscover)
	a.HandleAuthed("POST /api/v1/cloud-accounts/{id}/sync", m.handleTriggerSync)
	a.HandleAuthed("GET /api/v1/cloud-accounts/sync/{job_id}", m.handleGetSyncStatus)

	// Cloud Resources
	a.HandleAuthed("GET /api/v1/cloud-resources", m.handleListResources)
	a.HandleAuthed("GET /api/v1/cloud-resources/{id}", m.handleGetResource)
	a.HandleAuthed("POST /api/v1/cloud-resources/{id}/action", m.handleActionResource)
}

// HTTP helpers
func jsonSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func jsonError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": logx.Redact(msg),
		},
	})
}

// Handlers for Credentials
func (m *Module) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	list, err := m.credStore.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if list == nil {
		list = []credentials.Summary{}
	}
	jsonSuccess(w, http.StatusOK, map[string]any{
		"items":     list,
		"total":     len(list),
		"page":      1,
		"page_size": len(list),
	})
}

func (m *Module) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		CredKind        string `json:"cred_kind"`
		AccessKeyID     string `json:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Name == "" || req.CredKind == "" {
		jsonError(w, http.StatusBadRequest, "invalid_params", "name and cred_kind are required")
		return
	}

	var payloadBytes []byte
	if req.CredKind == "aliyun_ak" {
		if req.AccessKeyID == "" || req.AccessKeySecret == "" {
			jsonError(w, http.StatusBadRequest, "invalid_params", "access_key_id and access_key_secret are required")
			return
		}
		payloadBytes, _ = json.Marshal(credentials.AliyunAKPayload{
			AccessKeyID:     req.AccessKeyID,
			AccessKeySecret: req.AccessKeySecret,
		})
	} else {
		jsonError(w, http.StatusBadRequest, "unsupported_kind", "only aliyun_ak is supported")
		return
	}

	summary, err := m.credStore.Create(r.Context(), req.Name, req.CredKind, payloadBytes)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}

	jsonSuccess(w, http.StatusCreated, summary)
}

func (m *Module) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "invalid_params", "id is required")
		return
	}

	err := m.credStore.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, credentials.ErrInUse) {
			jsonError(w, http.StatusConflict, "conflict", "credential is in use by a cloud account and cannot be deleted")
			return
		}
		if errors.Is(err, credentials.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "credential not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}

	jsonSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

// Handlers for Cloud Accounts
func (m *Module) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := m.svc.ListAccounts(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if list == nil {
		list = []CloudAccount{}
	}
	jsonSuccess(w, http.StatusOK, map[string]any{
		"items":     list,
		"total":     len(list),
		"page":      1,
		"page_size": len(list),
	})
}

func (m *Module) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var acc CloudAccount
	if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if acc.ProviderCode == "" {
		acc.ProviderCode = "aliyun"
	}
	res, err := m.svc.CreateAccount(r.Context(), acc)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	jsonSuccess(w, http.StatusCreated, res)
}

func (m *Module) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := m.svc.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	jsonSuccess(w, http.StatusOK, res)
}

func (m *Module) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var acc CloudAccount
	if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	res, err := m.svc.UpdateAccount(r.Context(), id, acc)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		jsonError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}
	jsonSuccess(w, http.StatusOK, res)
}

func (m *Module) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := m.svc.DeleteAccount(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	jsonSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

// Discover & Sync Handlers
func (m *Module) handleListRegions(w http.ResponseWriter, r *http.Request) {
	credID := r.URL.Query().Get("credential_id")
	regions, err := m.svc.ListRegions(r.Context(), credID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "list_regions_failed", err.Error())
		return
	}
	if regions == nil {
		regions = []provider.Region{}
	}
	jsonSuccess(w, http.StatusOK, map[string]any{
		"items":     regions,
		"total":     len(regions),
		"page":      1,
		"page_size": len(regions),
	})
}

func (m *Module) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CredentialID string   `json:"credential_id"`
		Regions      []string `json:"regions"`
		AccountSite  string   `json:"account_site"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.CredentialID == "" {
		jsonError(w, http.StatusBadRequest, "invalid_params", "credential_id is required")
		return
	}

	resources, err := m.svc.DiscoverAccount(r.Context(), req.CredentialID, req.Regions, req.AccountSite)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "discover_failed", err.Error())
		return
	}

	if resources == nil {
		resources = []provider.NormalizedResource{}
	}
	jsonSuccess(w, http.StatusOK, map[string]any{
		"items":     resources,
		"total":     len(resources),
		"page":      1,
		"page_size": len(resources),
	})
}

func (m *Module) handleTriggerSync(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobID, err := m.svc.TriggerSync(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}

	jsonSuccess(w, http.StatusAccepted, map[string]any{
		"job_id": jobID,
		"status": "running",
	})
}

func (m *Module) handleGetSyncStatus(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	status, err := m.svc.GetSyncStatus(jobID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "job not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	jsonSuccess(w, http.StatusOK, status)
}

// Handlers for Cloud Resources
func (m *Module) handleListResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")
	providerCode := q.Get("provider_code")
	resKind := q.Get("res_kind")
	region := q.Get("region")
	status := q.Get("status")

	list, err := m.svc.ListResources(r.Context(), accountID, providerCode, resKind, region, status)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if list == nil {
		list = []CloudResource{}
	}
	jsonSuccess(w, http.StatusOK, map[string]any{
		"items":     list,
		"total":     len(list),
		"page":      1,
		"page_size": len(list),
	})
}

func (m *Module) handleGetResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := m.svc.GetResource(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	jsonSuccess(w, http.StatusOK, res)
}

func (m *Module) handleActionResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Action string `json:"action"` // "start" | "stop"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Action != "start" && req.Action != "stop" {
		jsonError(w, http.StatusBadRequest, "invalid_action", "action must be 'start' or 'stop'")
		return
	}

	actResp, err := m.svc.ActionResource(r.Context(), id, req.Action)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "action_failed", err.Error())
		return
	}

	jsonSuccess(w, http.StatusOK, actResp)
}
