package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"dash/internal/api/events"
	guardapi "dash/internal/api/guard"
	jobsapi "dash/internal/api/jobs"
	notifyapi "dash/internal/api/notify"
	"dash/internal/app"
	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/db"
	eventmodel "dash/internal/events"
	guardmodel "dash/internal/guard"
	"dash/internal/inventory"
	jobsmodel "dash/internal/jobs"
	notifymodel "dash/internal/notify"
	"dash/internal/provider"
	"dash/internal/settings"
)

const (
	fixedTimeMs = int64(1757222400000) // 2025-09-07 05:20:00 UTC
)

var fixedTime = time.UnixMilli(fixedTimeMs).UTC()

type fixtureTarget struct {
	file   string
	method string
	path   string
	body   string
	empty  bool
}

func main() {
	checkFlag := flag.Bool("check", false, "check if fixtures match server responses without updating")
	flag.Parse()

	// Locate fixtures directory relative to repo root
	repoRoot := "."
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(repoRoot, "web", "test", "fixtures", "contracts")); err == nil {
			break
		}
		repoRoot = filepath.Join("..", repoRoot)
	}
	fixturesDir := filepath.Join(repoRoot, "web", "test", "fixtures", "contracts")

	targets := []fixtureTarget{
		// Phase 1 fixtures
		{file: "healthz.json", method: "GET", path: "/healthz"},
		{file: "settings.json", method: "GET", path: "/api/v1/settings"},
		{file: "replace_tags.json", method: "POST", path: "/api/v1/nodes/01M1ZA80A5J7VCHRZTZ09MR0QE/tags", body: `{"tag_ids":["tag1"]}`},
		{file: "nodes.json", method: "GET", path: "/api/v1/nodes", empty: false},
		{file: "nodes_empty.json", method: "GET", path: "/api/v1/nodes", empty: true},
		{file: "groups.json", method: "GET", path: "/api/v1/node-groups", empty: false},
		{file: "groups_empty.json", method: "GET", path: "/api/v1/node-groups", empty: true},
		{file: "tags.json", method: "GET", path: "/api/v1/tags", empty: false},
		{file: "tags_empty.json", method: "GET", path: "/api/v1/tags", empty: true},
		{file: "enroll_tokens.json", method: "GET", path: "/api/v1/enroll-tokens", empty: false},
		{file: "enroll_tokens_empty.json", method: "GET", path: "/api/v1/enroll-tokens", empty: true},
		{file: "events.json", method: "GET", path: "/api/v1/events", empty: false},
		{file: "events_empty.json", method: "GET", path: "/api/v1/events", empty: true},
		{file: "event_types.json", method: "GET", path: "/api/v1/event-types", empty: false},

		// Phase 2 fixtures
		{file: "channels.json", method: "GET", path: "/api/v1/notify/channels", empty: false},
		{file: "channels_empty.json", method: "GET", path: "/api/v1/notify/channels", empty: true},
		{file: "rules.json", method: "GET", path: "/api/v1/notify/rules", empty: false},
		{file: "rules_empty.json", method: "GET", path: "/api/v1/notify/rules", empty: true},
		{file: "deliveries.json", method: "GET", path: "/api/v1/notify/deliveries", empty: false},
		{file: "deliveries_empty.json", method: "GET", path: "/api/v1/notify/deliveries", empty: true},
		{file: "credentials.json", method: "GET", path: "/api/v1/credentials", empty: false},
		{file: "credentials_empty.json", method: "GET", path: "/api/v1/credentials", empty: true},
		{file: "cloud_accounts.json", method: "GET", path: "/api/v1/cloud-accounts", empty: false},
		{file: "cloud_accounts_empty.json", method: "GET", path: "/api/v1/cloud-accounts", empty: true},
		{file: "cloud_resources.json", method: "GET", path: "/api/v1/cloud-resources", empty: false},
		{file: "cloud_resources_empty.json", method: "GET", path: "/api/v1/cloud-resources", empty: true},
		{file: "jobs.json", method: "GET", path: "/api/v1/jobs", empty: false},
		{file: "jobs_empty.json", method: "GET", path: "/api/v1/jobs", empty: true},
		{file: "job_kinds.json", method: "GET", path: "/api/v1/jobs/kinds", empty: false},
		{file: "guard_overview.json", method: "GET", path: "/api/v1/guard/overview", empty: false},
		{file: "guard_cycles.json", method: "GET", path: "/api/v1/guard/cycles", empty: false},
		{file: "guard_cycles_empty.json", method: "GET", path: "/api/v1/guard/cycles", empty: true},
	}

	hasDiff := false

	for _, tgt := range targets {
		outBytes, err := generateResponse(tgt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating fixture for %s: %v\n", tgt.file, err)
			os.Exit(1)
		}

		targetPath := filepath.Join(fixturesDir, tgt.file)

		if *checkFlag {
			existing, err := os.ReadFile(targetPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ 服务端响应变了，请跑 make fixtures 并检查前端是否要跟着改 (缺失: %s)\n", tgt.file)
				hasDiff = true
				continue
			}
			if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(outBytes)) {
				fmt.Fprintf(os.Stderr, "❌ 服务端响应变了，请跑 make fixtures 并检查前端是否要跟着改 (不匹配: %s)\n", tgt.file)
				hasDiff = true
			}
		} else {
			if err := os.WriteFile(targetPath, outBytes, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to write fixture %s: %v\n", tgt.file, err)
				os.Exit(1)
			}
			fmt.Printf("Generated %s\n", tgt.file)
		}
	}

	if *checkFlag && hasDiff {
		os.Exit(1)
	}
}

func generateResponse(tgt fixtureTarget) ([]byte, error) {
	a := &app.App{
		Mux:             http.NewServeMux(),
		Version:         "v1.0.0-contract-test",
		DistFingerprint: "",
	}
	a.HealthCheckPing = func(ctx context.Context) error { return nil }
	a.HandlePublic("/healthz", a.HealthzHandler())
	a.SetAuthMiddleware(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			next(w, r)
		}
	})

	// Inventory
	setupInventory(a, tgt.empty)

	// Events
	events.RegisterAppRoutes(a, &fakeEventsStore{empty: tgt.empty})

	// Settings
	settingsSvc := settings.NewService(nil, nil)
	settingsSvc.GetSettingsFn = func(ctx context.Context) (*settings.SystemSettings, error) {
		return &settings.SystemSettings{
			SiteDomain:           "custom.domain.org",
			ConsoleDomain:        "dash.us.workbench.work",
			RetentionRawDays:     3,
			Retention1mDays:      30,
			Retention1hDays:      365,
			Retention1dDays:      0,
			CollectIntervalFastS: 2,
			CollectIntervalSlowS: 30,
			CollectEnableConns:   true,
		}, nil
	}
	a.HandleAuthed("GET /api/v1/settings", settingsSvc.HandleGet)

	// Notify
	notifyapi.RegisterAppRoutes(a, &fakeNotifyStore{empty: tgt.empty}, nil)

	// Cloud
	cloudMod := cloud.NewModuleWithServices(&fakeCredStore{empty: tgt.empty}, &fakeCloudSvc{empty: tgt.empty})
	cloudMod.RegisterRoutes(a)

	// Jobs
	jobsH := jobsapi.NewHandlerWithStore(&fakeJobsStore{empty: tgt.empty}, &fakeJobsRegistry{})
	jobsH.RegisterAppRoutes(a)

	// Guard
	guardH := guardapi.NewHandler(&fakeGuardEngine{empty: tgt.empty}, &fakeGuardStore{empty: tgt.empty}, &fakeCloudSvc{empty: tgt.empty}, nil)
	guardH.NowFunc = func() time.Time { return fixedTime }
	guardH.RegisterAppRoutes(a)

	var reqBody *bytes.Reader
	if tgt.body != "" {
		reqBody = bytes.NewReader([]byte(tgt.body))
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(tgt.method, tgt.path, reqBody)
	if tgt.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()

	a.Mux.ServeHTTP(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		return nil, fmt.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}

	var parsed any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("invalid json: %w (body: %s)", err, rec.Body.String())
	}

	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return nil, err
	}
	pretty = append(pretty, '\n')
	return pretty, nil
}

func setupInventory(a *app.App, empty bool) {
	inv := inventory.NewModule()
	_ = inv.Register(a)

	grpID := "01M1ZA80A53NBT8REFQXVA3H8F"
	tagID := "01M1ZA90PE9W90K26TVG96Q1AA"

	inv.TagService().ReplaceNodeTagsFn = func(ctx context.Context, nodeID string, tagIDs []string, actorKind, actorID, ip string) error {
		return nil
	}

	if empty {
		inv.NodeService().ListNodesFn = func(ctx context.Context, f inventory.ListNodesFilter) (*inventory.PageResult, error) {
			return &inventory.PageResult{
				Items:    []*inventory.Node{},
				Total:    0,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.GroupService().ListGroupsFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			return &inventory.PageResult{
				Items:    []inventory.NodeGroup{},
				Total:    0,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.TagService().ListTagsFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			return &inventory.PageResult{
				Items:    []inventory.Tag{},
				Total:    0,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.EnrollService().ListTokensFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			return &inventory.PageResult{
				Items:    []inventory.EnrollToken{},
				Total:    0,
				Page:     1,
				PageSize: 50,
			}, nil
		}
	} else {
		inv.NodeService().ListNodesFn = func(ctx context.Context, f inventory.ListNodesFilter) (*inventory.PageResult, error) {
			seen := fixedTimeMs
			return &inventory.PageResult{
				Items: []*inventory.Node{
					{
						ID:           "01M1ZA80A5J7VCHRZTZ09MR0QE",
						Name:         "node-01M1ZA",
						NodeGroupID:  &grpID,
						DisplayOrder: 0,
						IsHidden:     false,
						ConnState:    "offline",
						LastSeenAtMs: &seen,
						CreatedAtMs:  fixedTimeMs,
						UpdatedAtMs:  fixedTimeMs,
						Group: &inventory.GroupInfo{
							ID:   grpID,
							Name: "grp-01M1ZA",
						},
						Tags: []*inventory.Tag{},
					},
					{
						ID:           "01M1ZA90PEHNMDXKC1ZCST7J8H",
						Name:         "node-01M1ZA90Q0Z67TX15FNWXGHQXG",
						NodeGroupID:  &grpID,
						DisplayOrder: 0,
						IsHidden:     false,
						ConnState:    "offline",
						LastSeenAtMs: &seen,
						CreatedAtMs:  fixedTimeMs,
						UpdatedAtMs:  fixedTimeMs,
						Group: &inventory.GroupInfo{
							ID:   grpID,
							Name: "grp-01M1ZA",
						},
						Tags: []*inventory.Tag{
							{
								ID:          tagID,
								Name:        "tag-01M1ZA",
								CreatedAtMs: fixedTimeMs,
								UpdatedAtMs: fixedTimeMs,
							},
						},
					},
				},
				Total:    2,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.GroupService().ListGroupsFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			return &inventory.PageResult{
				Items: []inventory.NodeGroup{
					{
						ID:           grpID,
						Name:         "grp-01M1ZA",
						DisplayOrder: 10,
						CreatedAtMs:  fixedTimeMs,
						UpdatedAtMs:  fixedTimeMs,
						NodeCount:    2,
					},
				},
				Total:    1,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.TagService().ListTagsFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			color := "#3b82f6"
			return &inventory.PageResult{
				Items: []inventory.Tag{
					{
						ID:          tagID,
						Name:        "tag-01M1ZA",
						Color:       &color,
						CreatedAtMs: fixedTimeMs,
						UpdatedAtMs: fixedTimeMs,
						NodeCount:   1,
					},
				},
				Total:    1,
				Page:     1,
				PageSize: 50,
			}, nil
		}
		inv.EnrollService().ListTokensFn = func(ctx context.Context, page, pageSize int) (*inventory.PageResult, error) {
			presetName := "default-preset"
			return &inventory.PageResult{
				Items: []inventory.EnrollToken{
					{
						ID:            "01M1ZA80A5J7VCHRZTZ09MR0QE",
						PresetName:    &presetName,
						PresetGroupID: &grpID,
						ExpiresAtMs:   fixedTimeMs + 86400000,
						CreatedAtMs:   fixedTimeMs,
						IsUsed:        false,
						IsExpired:     false,
					},
				},
				Total:    1,
				Page:     1,
				PageSize: 50,
			}, nil
		}
	}
}

// fakeEventsStore
type fakeEventsStore struct {
	empty bool
}

func (s *fakeEventsStore) ListEvents(ctx context.Context, f eventmodel.Filter) ([]eventmodel.EventRecord, int, error) {
	if s.empty {
		return []eventmodel.EventRecord{}, 0, nil
	}
	return []eventmodel.EventRecord{
		{
			ID:           "01M1ZA80A5J7VCHRZTZ09MR0QE",
			Type:         "node.offline",
			Severity:     "warning",
			SourceModule: "inventory",
			TargetKind:   "node",
			TargetID:     "01M1ZA80A5J7VCHRZTZ09MR0QE",
			Title:        "Node Offline",
			IsRead:       false,
			OccurredAtMs: fixedTimeMs,
			CreatedAtMs:  fixedTimeMs,
		},
	}, 1, nil
}

func (s *fakeEventsStore) MarkRead(ctx context.Context, req eventmodel.MarkReadRequest) (int64, error) {
	return 1, nil
}

func (s *fakeEventsStore) GetUnreadCount(ctx context.Context) (int64, error) {
	if s.empty {
		return 0, nil
	}
	return 1, nil
}

func (s *fakeEventsStore) ListEventTypes(ctx context.Context) ([]eventmodel.TypeDef, error) {
	return []eventmodel.TypeDef{
		{
			Type:               "node.offline",
			DisplayName:        "Node Offline",
			DefaultSeverity:    "warning",
			Severity:           "warning",
			DefaultDisposition: "alert",
			Disposition:        "alert",
			Description:        "Node offline alert",
			IsBuiltin:          true,
			UpdatedAtMs:        fixedTimeMs,
		},
	}, nil
}

func (s *fakeEventsStore) UpdateEventType(ctx context.Context, eventType, severity, disposition string) (*eventmodel.TypeDef, error) {
	return nil, nil
}

// fakeNotifyStore
type fakeNotifyStore struct {
	empty bool
}

func (s *fakeNotifyStore) ListChannels(ctx context.Context) ([]*notifymodel.Channel, error) {
	if s.empty {
		return []*notifymodel.Channel{}, nil
	}
	return []*notifymodel.Channel{
		{
			ID:           "01M1ZA80A5J7VCHRZTZ09MR0QE",
			Name:         "Telegram Alert Bot",
			Kind:         "telegram",
			ConfigJSON:   `{"chat_id":"-1001234567890"}`,
			MaskedSecret: "bot123456:ABC-DEF",
			IsEnabled:    true,
			CreatedAtMs:  fixedTimeMs,
			UpdatedAtMs:  fixedTimeMs,
		},
	}, nil
}

func (s *fakeNotifyStore) GetChannel(ctx context.Context, id string) (*notifymodel.Channel, error) {
	return nil, nil
}
func (s *fakeNotifyStore) CreateChannel(ctx context.Context, name, kind, secret, configJSON string) (*notifymodel.Channel, error) {
	return nil, nil
}
func (s *fakeNotifyStore) UpdateChannel(ctx context.Context, id, name string, isEnabled bool, secret, configJSON string) (*notifymodel.Channel, error) {
	return nil, nil
}
func (s *fakeNotifyStore) DeleteChannel(ctx context.Context, id string) error { return nil }

func (s *fakeNotifyStore) ListRules(ctx context.Context) ([]*notifymodel.Rule, error) {
	if s.empty {
		return []*notifymodel.Rule{}, nil
	}
	qStart := 1380
	qEnd := 420
	return []*notifymodel.Rule{
		{
			ID:              "01M1ZA80A5J7VCHRZTZ09MR0QE",
			Name:            "Critical Alert Routing",
			EventPattern:    "node.*",
			MinSeverity:     "warning",
			NotifyChannelID: "01M1ZA80A5J7VCHRZTZ09MR0QE",
			ThrottleS:       300,
			QuietStartMin:   &qStart,
			QuietEndMin:     &qEnd,
			IsEnabled:       true,
			CreatedAtMs:     fixedTimeMs,
			UpdatedAtMs:     fixedTimeMs,
		},
	}, nil
}

func (s *fakeNotifyStore) GetRule(ctx context.Context, id string) (*notifymodel.Rule, error) {
	return nil, nil
}
func (s *fakeNotifyStore) CreateRule(ctx context.Context, rule *notifymodel.Rule) (*notifymodel.Rule, error) {
	return nil, nil
}
func (s *fakeNotifyStore) UpdateRule(ctx context.Context, rule *notifymodel.Rule) (*notifymodel.Rule, error) {
	return nil, nil
}
func (s *fakeNotifyStore) DeleteRule(ctx context.Context, id string) error { return nil }

func (s *fakeNotifyStore) ListDeliveries(ctx context.Context, filter notifymodel.DeliveryFilter) ([]*notifymodel.Delivery, int, error) {
	if s.empty {
		return []*notifymodel.Delivery{}, 0, nil
	}
	return []*notifymodel.Delivery{
		{
			ID:           "01M1ZA80A5J7VCHRZTZ09MR0QE",
			EventID:      "01M1ZA80A5J7VCHRZTZ09MR0QE",
			RuleID:       "01M1ZA80A5J7VCHRZTZ09MR0QE",
			ChannelID:    "01M1ZA80A5J7VCHRZTZ09MR0QE",
			State:        "sent",
			Attempt:      1,
			RenderedText: "Node node-01M1ZA went offline",
			CreatedAtMs:  fixedTimeMs,
			UpdatedAtMs:  fixedTimeMs,
		},
	}, 1, nil
}

// fakeCredStore
type fakeCredStore struct {
	empty bool
}

func (s *fakeCredStore) List(ctx context.Context) ([]credentials.Summary, error) {
	if s.empty {
		return []credentials.Summary{}, nil
	}
	return []credentials.Summary{
		{
			ID:          "01M1ZA80A5J7VCHRZTZ09MR0QE",
			Name:        "aliyun-prod",
			CredKind:    "aliyun_ak",
			Fingerprint: "LTAI5t********99",
			CreatedAtMs: fixedTimeMs,
			UpdatedAtMs: fixedTimeMs,
		},
	}, nil
}
func (s *fakeCredStore) Create(ctx context.Context, name, credKind string, payload []byte) (*credentials.Summary, error) {
	return nil, nil
}
func (s *fakeCredStore) Delete(ctx context.Context, id string) error { return nil }

// fakeCloudSvc
type fakeCloudSvc struct {
	empty bool
}

func (s *fakeCloudSvc) EnsureBuiltinProviders(ctx context.Context) error { return nil }

func (s *fakeCloudSvc) ListAccounts(ctx context.Context) ([]cloud.CloudAccount, error) {
	if s.empty {
		return []cloud.CloudAccount{}, nil
	}
	return []cloud.CloudAccount{
		{
			ID:            "01M1ZA80A5J7VCHRZTZ09MR0QE",
			ProviderCode:  "aliyun",
			Name:          "prod-aliyun-main",
			CredentialID:  "01M1ZA80A5J7VCHRZTZ09MR0QE",
			DefaultRegion: "cn-hangzhou",
			AccountSite:   "china",
			IsEnabled:     true,
			LastSyncAtMs:  fixedTimeMs,
			CreatedAtMs:   fixedTimeMs,
			UpdatedAtMs:   fixedTimeMs,
		},
	}, nil
}

func (s *fakeCloudSvc) CreateAccount(ctx context.Context, acc cloud.CloudAccount) (*cloud.CloudAccount, error) {
	return nil, nil
}
func (s *fakeCloudSvc) GetAccount(ctx context.Context, id string) (*cloud.CloudAccount, error) {
	if s.empty {
		return nil, fmt.Errorf("account not found")
	}
	return &cloud.CloudAccount{
		ID:            "01M1ZA80A5J7VCHRZTZ09MR0QE",
		ProviderCode:  "aliyun",
		Name:          "prod-aliyun-main",
		CredentialID:  "01M1ZA80A5J7VCHRZTZ09MR0QE",
		DefaultRegion: "cn-hangzhou",
		AccountSite:   "china",
		IsEnabled:     true,
		CreatedAtMs:   fixedTimeMs,
		UpdatedAtMs:   fixedTimeMs,
	}, nil
}
func (s *fakeCloudSvc) UpdateAccount(ctx context.Context, id string, acc cloud.CloudAccount) (*cloud.CloudAccount, error) {
	return nil, nil
}
func (s *fakeCloudSvc) DeleteAccount(ctx context.Context, id string) error { return nil }
func (s *fakeCloudSvc) TriggerSync(ctx context.Context, accountID string) (string, error) {
	return "01M1ZA80A5J7VCHRZTZ09MR0QE", nil
}
func (s *fakeCloudSvc) GetSyncStatus(jobID string) (*cloud.SyncJobStatus, error) {
	return nil, nil
}
func (s *fakeCloudSvc) DiscoverAccount(ctx context.Context, credID string, regions []string, site string) ([]provider.NormalizedResource, error) {
	return nil, nil
}

func (s *fakeCloudSvc) ListResources(ctx context.Context, accountID, providerCode, resKind, region, status string) ([]cloud.CloudResource, error) {
	if s.empty {
		return []cloud.CloudResource{}, nil
	}
	return []cloud.CloudResource{
		{
			ID:             "01M1ZA80A5J7VCHRZTZ09MR0QE",
			CloudAccountID: "01M1ZA80A5J7VCHRZTZ09MR0QE",
			ProviderCode:   "aliyun",
			ResKind:        "ecs",
			ResRef:         "i-bp1abcdef123456",
			Name:           "web-app-01",
			Region:         "cn-hangzhou",
			Status:         "Running",
			PublicIPs:      `["120.76.1.2"]`,
			PrivateIPs:     `["172.16.0.10"]`,
			SpecsJSON:      `{"vcpu":2,"mem_mb":4096}`,
			CreatedAtMs:    fixedTimeMs,
			UpdatedAtMs:    fixedTimeMs,
		},
	}, nil
}

func (s *fakeCloudSvc) GetResource(ctx context.Context, id string) (*cloud.CloudResource, error) {
	if s.empty {
		return nil, fmt.Errorf("resource not found")
	}
	return &cloud.CloudResource{
		ID:             "01M1ZA80A5J7VCHRZTZ09MR0QE",
		CloudAccountID: "01M1ZA80A5J7VCHRZTZ09MR0QE",
		ProviderCode:   "aliyun",
		ResKind:        "ecs",
		ResRef:         "i-bp1abcdef123456",
		Name:           "web-app-01",
		Region:         "cn-hangzhou",
		Status:         "Running",
		PublicIPs:      `["120.76.1.2"]`,
		PrivateIPs:     `["172.16.0.10"]`,
		SpecsJSON:      `{"vcpu":2,"mem_mb":4096}`,
		CreatedAtMs:    fixedTimeMs,
		UpdatedAtMs:    fixedTimeMs,
	}, nil
}

func (s *fakeCloudSvc) ActionResource(ctx context.Context, id, action string) (*provider.ActionResponse, error) {
	return nil, nil
}

// fakeJobsStore
type fakeJobsStore struct {
	empty bool
}

func (s *fakeJobsStore) ListJobs(ctx context.Context, f jobsmodel.Filter) ([]*jobsmodel.Job, int, error) {
	if s.empty {
		return []*jobsmodel.Job{}, 0, nil
	}
	startedAt := fixedTimeMs
	finishedAt := fixedTimeMs + 5000
	return []*jobsmodel.Job{
		{
			ID:            "01M1ZA80A5J7VCHRZTZ09MR0QE",
			JobKind:       "guard.ecs_stop",
			JobState:      jobsmodel.StateSucceeded,
			TargetKind:    "cloud_resource",
			TargetID:      "01M1ZA80A5J7VCHRZTZ09MR0QE",
			ParamsJSON:    `{"action":"stop","reason":"schedule"}`,
			ResultJSON:    `{"status":"success"}`,
			Attempt:       1,
			MaxAttempt:    3,
			ScheduledAtMs: fixedTimeMs,
			StartedAtMs:   &startedAt,
			FinishedAtMs:  &finishedAt,
			CreatedAtMs:   fixedTimeMs,
			UpdatedAtMs:   fixedTimeMs + 5000,
		},
	}, 1, nil
}

func (s *fakeJobsStore) GetJob(ctx context.Context, id string) (*jobsmodel.Job, error) {
	return nil, nil
}

// fakeJobsRegistry
type fakeJobsRegistry struct{}

func (r *fakeJobsRegistry) List() []jobsmodel.JobDefinition {
	return []jobsmodel.JobDefinition{
		{
			Kind:        "guard.ecs_stop",
			Description: "Stop ECS instance",
			Timeout:     5 * time.Minute,
			MaxAttempt:  3,
			Steps: []jobsmodel.StepDef{
				{Name: "call_stop_api", Idempotent: true},
			},
		},
		{
			Kind:        "guard.ecs_start",
			Description: "Start ECS instance",
			Timeout:     5 * time.Minute,
			MaxAttempt:  3,
			Steps: []jobsmodel.StepDef{
				{Name: "call_start_api", Idempotent: true},
			},
		},
	}
}

// fakeGuardStore
type fakeGuardStore struct {
	empty bool
}

func (s *fakeGuardStore) DB() *db.DB { return nil }

func (s *fakeGuardStore) ListRules(ctx context.Context) (map[string]*guardmodel.GuardRule, error) {
	if s.empty {
		return map[string]*guardmodel.GuardRule{}, nil
	}
	schedStart := "09:00"
	schedStop := "18:00"
	return map[string]*guardmodel.GuardRule{
		"01M1ZA80A5J7VCHRZTZ09MR0QE": {
			ID:              "01M1ZA80A5J7VCHRZTZ09MR0QE",
			CloudResourceID: "01M1ZA80A5J7VCHRZTZ09MR0QE",
			IsEnabled:       true,
			ActionsEnabled:  true,
			TrafficAction:   "stop",
			ScheduleEnabled: true,
			ScheduleStart:   &schedStart,
			ScheduleStop:    &schedStop,
			ScheduleTZ:      "Asia/Shanghai",
			CreatedAtMs:     fixedTimeMs,
			UpdatedAtMs:     fixedTimeMs,
		},
	}, nil
}

func (s *fakeGuardStore) ListRecentCycles(ctx context.Context, limit, offset int) ([]guardmodel.GuardCycle, int, error) {
	if s.empty {
		return []guardmodel.GuardCycle{}, 0, nil
	}
	cdt := 12.50
	return []guardmodel.GuardCycle{
		{
			ID:             "01M1ZA80A5J7VCHRZTZ09MR0QE",
			CloudAccountID: "01M1ZA80A5J7VCHRZTZ09MR0QE",
			StartedAtMs:    fixedTimeMs,
			DurationMs:     320,
			CDTUsedGB:      &cdt,
			Evaluated:      1,
			Acted:          0,
			Failed:         0,
			CreatedAtMs:    fixedTimeMs,
		},
	}, 1, nil
}

func (s *fakeGuardStore) GetRuleByResourceID(ctx context.Context, resourceID string) (*guardmodel.GuardRule, error) {
	return nil, nil
}
func (s *fakeGuardStore) UpsertRule(ctx context.Context, rule *guardmodel.GuardRule) error {
	return nil
}
func (s *fakeGuardStore) GetAccountPolicy(ctx context.Context, accountID string) (*guardmodel.GuardAccountPolicy, error) {
	if s.empty {
		return nil, nil
	}
	limit := 100.0
	schedStart := "09:00"
	schedStop := "18:00"
	return &guardmodel.GuardAccountPolicy{
		CloudAccountID:  accountID,
		IsEnabled:       true,
		ActionsEnabled:  false,
		TrafficLimitGB:  &limit,
		TrafficAction:   "stop",
		WarnRatio:       0.8,
		ScheduleEnabled: true,
		ScheduleStart:   &schedStart,
		ScheduleStop:    &schedStop,
		ScheduleTZ:      "Asia/Shanghai",
		EvalIntervalS:   60,
		CreatedAtMs:     fixedTimeMs,
		UpdatedAtMs:     fixedTimeMs,
	}, nil
}
func (s *fakeGuardStore) ListAccountPolicies(ctx context.Context) (map[string]*guardmodel.GuardAccountPolicy, error) {
	if s.empty {
		return map[string]*guardmodel.GuardAccountPolicy{}, nil
	}
	limit := 100.0
	schedStart := "09:00"
	schedStop := "18:00"
	return map[string]*guardmodel.GuardAccountPolicy{
		"01M1ZA80A5J7VCHRZTZ09MR0QE": {
			CloudAccountID:  "01M1ZA80A5J7VCHRZTZ09MR0QE",
			IsEnabled:       true,
			ActionsEnabled:  false,
			TrafficLimitGB:  &limit,
			TrafficAction:   "stop",
			WarnRatio:       0.8,
			ScheduleEnabled: true,
			ScheduleStart:   &schedStart,
			ScheduleStop:    &schedStop,
			ScheduleTZ:      "Asia/Shanghai",
			EvalIntervalS:   60,
			CreatedAtMs:     fixedTimeMs,
			UpdatedAtMs:     fixedTimeMs,
		},
	}, nil
}
func (s *fakeGuardStore) UpsertAccountPolicy(ctx context.Context, p *guardmodel.GuardAccountPolicy) error {
	return nil
}

// fakeGuardEngine
type fakeGuardEngine struct {
	empty bool
}

func (e *fakeGuardEngine) EvaluateOnce(ctx context.Context, isDryRun bool) (*guardmodel.EvaluateResult, error) {
	if e.empty {
		return &guardmodel.EvaluateResult{
			CycleID:        "01M1ZA80A5J7VCHRZTZ09MR0QE",
			StartedAtMs:    fixedTimeMs,
			DurationMs:     50,
			IsDryRun:       isDryRun,
			EvaluatedCount: 0,
			ActedCount:     0,
			FailedCount:    0,
			Items:          []guardmodel.EvaluationItem{},
		}, nil
	}
	return &guardmodel.EvaluateResult{
		CycleID:        "01M1ZA80A5J7VCHRZTZ09MR0QE",
		StartedAtMs:    fixedTimeMs,
		DurationMs:     120,
		IsDryRun:       isDryRun,
		EvaluatedCount: 1,
		ActedCount:     1,
		FailedCount:    0,
		Items: []guardmodel.EvaluationItem{
			{
				AccountID:      "01M1ZA80A5J7VCHRZTZ09MR0QE",
				AccountName:    "prod-aliyun-main",
				ResourceID:     "01M1ZA80A5J7VCHRZTZ09MR0QE",
				ResourceName:   "web-app-01",
				ResourceRef:    "i-bp1abcdef123456",
				Region:         "cn-hangzhou",
				CurrentStatus:  "Running",
				ProposedAction: "stop",
				Reason:         "in schedule stop window (18:00 - 09:00)",
				CDTUsedGB:      12.5,
				UsagePercent:   62.5,
				ActionsEnabled: true,
				InScheduleStop: true,
				WouldExecute:   true,
			},
		},
	}, nil
}
