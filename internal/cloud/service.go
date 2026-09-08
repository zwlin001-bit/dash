package cloud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/logx"
	"dash/internal/provider"
	"dash/internal/ulid"
)

var (
	ErrNotFound = errors.New("cloud: not found")
)

type Service struct {
	db        *db.DB
	credStore *credentials.Store
	pm        *provider.Manager
	jobsMu    sync.RWMutex
	jobs      map[string]*SyncJobStatus
}

func NewService(d *db.DB, credStore *credentials.Store, pm *provider.Manager) *Service {
	return &Service{
		db:        d,
		credStore: credStore,
		pm:        pm,
		jobs:      make(map[string]*SyncJobStatus),
	}
}

// EnsureBuiltinProviders initializes builtin providers like aliyun in the providers table.
func (s *Service) EnsureBuiltinProviders(ctx context.Context) error {
	now := time.Now().UnixMilli()
	q := `SELECT COUNT(*) FROM providers WHERE provider_code = ?`
	var count int
	if err := s.db.QueryRow(ctx, q, "aliyun").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		insertQ := `INSERT INTO providers (provider_code, display_name, exec_path, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?)`
		_, err := s.db.Exec(ctx, insertQ, "aliyun", "阿里云", "/usr/local/bin/dash-provider-aliyun", 1, now, now)
		if err != nil {
			return fmt.Errorf("cloud: insert builtin provider failed: %w", err)
		}
	} else {
		updateQ := `UPDATE providers SET exec_path = ?, updated_at_ms = ? WHERE provider_code = ? AND (exec_path = 'bin/dash-provider-aliyun' OR exec_path NOT LIKE '/%')`
		if _, err := s.db.Exec(ctx, updateQ, "/usr/local/bin/dash-provider-aliyun", now, "aliyun"); err != nil {
			return fmt.Errorf("cloud: update builtin provider exec_path failed: %w", err)
		}
	}
	return nil
}

func (s *Service) getProviderClient(ctx context.Context, providerCode string) (provider.ProviderClient, error) {
	var execPath string
	_ = s.db.QueryRow(ctx, `SELECT exec_path FROM providers WHERE provider_code = ?`, providerCode).Scan(&execPath)
	return s.pm.GetClient(ctx, providerCode, execPath)
}

// CreateAccount registers a new cloud account.
func (s *Service) CreateAccount(ctx context.Context, acc CloudAccount) (*CloudAccount, error) {
	acc.Name = strings.TrimSpace(acc.Name)
	if acc.Name == "" {
		return nil, errors.New("cloud: account name cannot be empty")
	}
	if acc.ProviderCode == "" {
		return nil, errors.New("cloud: provider_code cannot be empty")
	}
	if acc.CredentialID == "" {
		return nil, errors.New("cloud: credential_id cannot be empty")
	}

	// Verify credential exists
	if _, err := s.credStore.GetSummary(ctx, acc.CredentialID); err != nil {
		return nil, fmt.Errorf("cloud: invalid credential_id: %w", err)
	}

	acc.ID = ulid.New()
	now := time.Now().UnixMilli()
	acc.CreatedAtMs = now
	acc.UpdatedAtMs = now
	acc.IsEnabled = true
	if acc.AccountSite == "" {
		acc.AccountSite = "china"
	}

	q := `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, default_region, account_site, config_json, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, acc.ID, acc.ProviderCode, acc.Name, acc.CredentialID, acc.DefaultRegion, acc.AccountSite, acc.ConfigJSON, 1, now, now)
	if err != nil {
		return nil, fmt.Errorf("cloud: create account failed: %w", err)
	}

	return &acc, nil
}

// ListAccounts returns all cloud accounts.
func (s *Service) ListAccounts(ctx context.Context) ([]CloudAccount, error) {
	q := `SELECT id, provider_code, name, credential_id, default_region, account_site, config_json, is_enabled, last_sync_at_ms, created_at_ms, updated_at_ms
FROM cloud_accounts ORDER BY created_at_ms DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cloud: list accounts failed: %w", err)
	}
	defer rows.Close()

	var list []CloudAccount
	for rows.Next() {
		var a CloudAccount
		var defaultRegion, accountSite, configJSON sql.NullString
		var lastSync sql.NullInt64
		var isEnabled int
		if err := rows.Scan(&a.ID, &a.ProviderCode, &a.Name, &a.CredentialID, &defaultRegion, &accountSite, &configJSON, &isEnabled, &lastSync, &a.CreatedAtMs, &a.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("cloud: scan account failed: %w", err)
		}
		a.DefaultRegion = defaultRegion.String
		a.AccountSite = accountSite.String
		a.ConfigJSON = configJSON.String
		a.IsEnabled = (isEnabled == 1)
		if lastSync.Valid {
			a.LastSyncAtMs = lastSync.Int64
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []CloudAccount{}
	}
	return list, nil
}

// GetAccount retrieves a single cloud account.
func (s *Service) GetAccount(ctx context.Context, id string) (*CloudAccount, error) {
	q := `SELECT id, provider_code, name, credential_id, default_region, account_site, config_json, is_enabled, last_sync_at_ms, created_at_ms, updated_at_ms
FROM cloud_accounts WHERE id = ?`
	var a CloudAccount
	var defaultRegion, accountSite, configJSON sql.NullString
	var lastSync sql.NullInt64
	var isEnabled int
	err := s.db.QueryRow(ctx, q, id).Scan(&a.ID, &a.ProviderCode, &a.Name, &a.CredentialID, &defaultRegion, &accountSite, &configJSON, &isEnabled, &lastSync, &a.CreatedAtMs, &a.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("cloud: get account failed: %w", err)
	}
	a.DefaultRegion = defaultRegion.String
	a.AccountSite = accountSite.String
	a.ConfigJSON = configJSON.String
	a.IsEnabled = (isEnabled == 1)
	if lastSync.Valid {
		a.LastSyncAtMs = lastSync.Int64
	}
	return &a, nil
}

// UpdateAccount updates an existing cloud account.
func (s *Service) UpdateAccount(ctx context.Context, id string, acc CloudAccount) (*CloudAccount, error) {
	acc.Name = strings.TrimSpace(acc.Name)
	if acc.Name == "" {
		return nil, errors.New("cloud: account name cannot be empty")
	}
	now := time.Now().UnixMilli()

	q := `UPDATE cloud_accounts SET name = ?, default_region = ?, account_site = ?, config_json = ?, is_enabled = ?, updated_at_ms = ?
WHERE id = ?`
	isEnabled := 0
	if acc.IsEnabled {
		isEnabled = 1
	}
	res, err := s.db.Exec(ctx, q, acc.Name, acc.DefaultRegion, acc.AccountSite, acc.ConfigJSON, isEnabled, now, id)
	if err != nil {
		return nil, fmt.Errorf("cloud: update account failed: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrNotFound
	}
	return s.GetAccount(ctx, id)
}

// DeleteAccount deletes an account and its associated resources.
func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	_, _ = s.db.Exec(ctx, "DELETE FROM cloud_resources WHERE cloud_account_id = ?", id)
	res, err := s.db.Exec(ctx, "DELETE FROM cloud_accounts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("cloud: delete account failed: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListResources queries cloud resources with optional filters.
func (s *Service) ListResources(ctx context.Context, accountID, providerCode, resKind, region, status string) ([]CloudResource, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, public_ips, private_ips, specs_json, billing_json, attrs_json, node_id, synced_at_ms, is_deleted, created_at_ms, updated_at_ms
FROM cloud_resources WHERE 1=1`)

	var args []any
	if accountID != "" {
		sb.WriteString(" AND cloud_account_id = ?")
		args = append(args, accountID)
	}
	if providerCode != "" {
		sb.WriteString(" AND provider_code = ?")
		args = append(args, providerCode)
	}
	if resKind != "" {
		sb.WriteString(" AND res_kind = ?")
		args = append(args, resKind)
	}
	if region != "" {
		sb.WriteString(" AND region = ?")
		args = append(args, region)
	}
	if status != "" {
		sb.WriteString(" AND status = ?")
		args = append(args, status)
	}
	sb.WriteString(" ORDER BY created_at_ms DESC")

	rows, err := s.db.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("cloud: list resources failed: %w", err)
	}
	defer rows.Close()

	var list []CloudResource
	for rows.Next() {
		var r CloudResource
		var name, regionVal, statusVal, pubIPs, privIPs, specsJSON, billJSON, attrsJSON, nodeID sql.NullString
		var isDel int
		if err := rows.Scan(&r.ID, &r.CloudAccountID, &r.ProviderCode, &r.ResKind, &r.ResRef, &name, &regionVal, &statusVal, &pubIPs, &privIPs, &specsJSON, &billJSON, &attrsJSON, &nodeID, &r.SyncedAtMs, &isDel, &r.CreatedAtMs, &r.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("cloud: scan resource failed: %w", err)
		}
		r.Name = name.String
		r.Region = regionVal.String
		r.Status = statusVal.String
		r.PublicIPs = pubIPs.String
		r.PrivateIPs = privIPs.String
		r.SpecsJSON = specsJSON.String
		r.BillingJSON = billJSON.String
		r.AttrsJSON = attrsJSON.String
		r.NodeID = nodeID.String
		r.IsDeleted = (isDel == 1)
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []CloudResource{}
	}
	return list, nil
}

// GetResource retrieves a single resource by ID.
func (s *Service) GetResource(ctx context.Context, id string) (*CloudResource, error) {
	q := `SELECT id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, public_ips, private_ips, specs_json, billing_json, attrs_json, node_id, synced_at_ms, is_deleted, created_at_ms, updated_at_ms
FROM cloud_resources WHERE id = ?`
	var r CloudResource
	var name, regionVal, statusVal, pubIPs, privIPs, specsJSON, billJSON, attrsJSON, nodeID sql.NullString
	var isDel int
	err := s.db.QueryRow(ctx, q, id).Scan(&r.ID, &r.CloudAccountID, &r.ProviderCode, &r.ResKind, &r.ResRef, &name, &regionVal, &statusVal, &pubIPs, &privIPs, &specsJSON, &billJSON, &attrsJSON, &nodeID, &r.SyncedAtMs, &isDel, &r.CreatedAtMs, &r.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("cloud: get resource failed: %w", err)
	}
	r.Name = name.String
	r.Region = regionVal.String
	r.Status = statusVal.String
	r.PublicIPs = pubIPs.String
	r.PrivateIPs = privIPs.String
	r.SpecsJSON = specsJSON.String
	r.BillingJSON = billJSON.String
	r.AttrsJSON = attrsJSON.String
	r.NodeID = nodeID.String
	r.IsDeleted = (isDel == 1)
	return &r, nil
}

// ActionResource executes start/stop on a cloud resource.
func (s *Service) ActionResource(ctx context.Context, id, action string) (*provider.ActionResponse, error) {
	r, err := s.GetResource(ctx, id)
	if err != nil {
		return nil, err
	}

	acc, err := s.GetAccount(ctx, r.CloudAccountID)
	if err != nil {
		return nil, err
	}

	_, pt, err := s.credStore.GetDecrypted(ctx, acc.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("cloud: decrypt credential failed: %w", err)
	}

	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		return nil, fmt.Errorf("cloud: parse credential json: %w", err)
	}

	client, err := s.getProviderClient(ctx, r.ProviderCode)
	if err != nil {
		return nil, fmt.Errorf("cloud: get provider client %s failed: %w", r.ProviderCode, err)
	}

	return client.Action(ctx, provider.ActionParams{
		Credential: credMap,
		Region:     r.Region,
		Kind:       r.ResKind,
		Ref:        r.ResRef,
		Action:     action,
	})
}

// DiscoverAccount performs auto-discovery across regions.
func (s *Service) DiscoverAccount(ctx context.Context, credentialID string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
	_, pt, err := s.credStore.GetDecrypted(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("cloud: decrypt credential failed: %w", err)
	}

	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		return nil, fmt.Errorf("cloud: parse credential: %w", err)
	}

	client, err := s.getProviderClient(ctx, "aliyun")
	if err != nil {
		return nil, fmt.Errorf("cloud: get provider client: %w", err)
	}

	return client.Discover(ctx, credMap, regions, accountSite)
}

// TriggerSync starts an asynchronous sync job and returns the job ID immediately.
func (s *Service) TriggerSync(ctx context.Context, accountID string) (string, error) {
	if _, err := s.GetAccount(ctx, accountID); err != nil {
		return "", err
	}

	jobID := ulid.New()
	status := &SyncJobStatus{
		JobID:     jobID,
		AccountID: accountID,
		State:     "running",
	}

	s.jobsMu.Lock()
	s.jobs[jobID] = status
	s.jobsMu.Unlock()

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		s.executeSync(bgCtx, jobID, accountID)
	}()

	return jobID, nil
}

// GetSyncStatus retrieves the state of a sync job.
func (s *Service) GetSyncStatus(jobID string) (*SyncJobStatus, error) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	if st, ok := s.jobs[jobID]; ok {
		return st, nil
	}
	return nil, ErrNotFound
}

// executeSync performs the actual cloud sync operation.
func (s *Service) executeSync(ctx context.Context, jobID, accountID string) {
	acc, err := s.GetAccount(ctx, accountID)
	if err != nil {
		s.updateJobFailed(jobID, err.Error())
		return
	}

	_, pt, err := s.credStore.GetDecrypted(ctx, acc.CredentialID)
	if err != nil {
		s.updateJobFailed(jobID, "decrypt credential failed: "+err.Error())
		return
	}

	var credMap map[string]string
	if err := json.Unmarshal(pt, &credMap); err != nil {
		s.updateJobFailed(jobID, "unmarshal credential failed: "+err.Error())
		return
	}

	client, err := s.getProviderClient(ctx, acc.ProviderCode)
	if err != nil {
		s.updateJobFailed(jobID, "get provider client failed: "+err.Error())
		return
	}

	// 1. Fetch CDT traffic (account-level, called once per account)
	cdtRes, err := client.GetCDTTraffic(ctx, credMap)
	var trafficBytes int64
	if err == nil && cdtRes != nil {
		trafficBytes = cdtRes.TrafficBytes
	} else if err != nil {
		logx.Warn("fetch cdt traffic failed during sync", "account", acc.Name, "err", logx.Redact(err.Error()))
	}

	// 2. Discover ECS instances across regions
	regions := []string{acc.DefaultRegion}
	if acc.DefaultRegion == "" {
		regions = []string{"cn-hangzhou", "cn-shanghai", "cn-beijing", "cn-shenzhen", "cn-hongkong", "ap-southeast-1"}
	}

	resources, err := client.Discover(ctx, credMap, regions, acc.AccountSite)
	if err != nil {
		s.updateJobFailed(jobID, "discover resources failed: "+err.Error())
		return
	}

	now := time.Now().UnixMilli()

	// 3. Upsert resources into cloud_resources table
	for _, nr := range resources {
		specsB, _ := json.Marshal(nr.Specs)
		billB, _ := json.Marshal(nr.Billing)
		attrsB, _ := json.Marshal(nr.Attrs)
		pubIPs := strings.Join(nr.PublicIPs, ",")
		privIPs := strings.Join(nr.PrivateIPs, ",")

		// Check if resource exists
		var existingID string
		qCheck := "SELECT id FROM cloud_resources WHERE cloud_account_id = ? AND res_kind = ? AND res_ref = ?"
		err := s.db.QueryRow(ctx, qCheck, acc.ID, nr.Kind, nr.Ref).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			logx.Warn("check existing cloud resource failed", "err", err)
			continue
		}

		if existingID != "" {
			qUpd := `UPDATE cloud_resources SET name = ?, region = ?, status = ?, public_ips = ?, private_ips = ?, specs_json = ?, billing_json = ?, attrs_json = ?, synced_at_ms = ?, is_deleted = 0, updated_at_ms = ?
WHERE id = ?`
			_, _ = s.db.Exec(ctx, qUpd, nr.Name, nr.Region, nr.Status, pubIPs, privIPs, string(specsB), string(billB), string(attrsB), now, now, existingID)
		} else {
			resID := ulid.New()
			qIns := `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, public_ips, private_ips, specs_json, billing_json, attrs_json, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`
			_, _ = s.db.Exec(ctx, qIns, resID, acc.ID, acc.ProviderCode, nr.Kind, nr.Ref, nr.Name, nr.Region, nr.Status, pubIPs, privIPs, string(specsB), string(billB), string(attrsB), now, now, now)
		}
	}

	// 4. Update cloud_accounts.last_sync_at_ms
	// Store traffic bytes in config_json or update last_sync_at_ms
	cfgData := make(map[string]any)
	if acc.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(acc.ConfigJSON), &cfgData)
	}
	cfgData["cdt_traffic_bytes"] = trafficBytes
	cfgDataB, _ := json.Marshal(cfgData)

	_, _ = s.db.Exec(ctx, "UPDATE cloud_accounts SET last_sync_at_ms = ?, config_json = ?, updated_at_ms = ? WHERE id = ?", now, string(cfgDataB), now, acc.ID)

	s.jobsMu.Lock()
	if st, ok := s.jobs[jobID]; ok {
		st.State = "succeeded"
		st.SyncedCount = len(resources)
		st.TrafficBytes = trafficBytes
		st.Message = fmt.Sprintf("Successfully synced %d resources", len(resources))
	}
	s.jobsMu.Unlock()
}

func (s *Service) updateJobFailed(jobID, msg string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if st, ok := s.jobs[jobID]; ok {
		st.State = "failed"
		st.Message = logx.Redact(msg)
	}
}
