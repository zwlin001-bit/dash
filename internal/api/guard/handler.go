package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dash/internal/app"
	"dash/internal/audit"
	"dash/internal/auth"
	"dash/internal/cloud"
	"dash/internal/db"
	"dash/internal/guard"
	"dash/internal/jobs"
	"dash/internal/logx"
	"dash/internal/ulid"
)

type Store interface {
	DB() *db.DB
	ListRules(ctx context.Context) (map[string]*guard.GuardRule, error)
	ListRecentCycles(ctx context.Context, limit, offset int) ([]guard.GuardCycle, int, error)
	GetRuleByResourceID(ctx context.Context, resourceID string) (*guard.GuardRule, error)
	UpsertRule(ctx context.Context, rule *guard.GuardRule) error
	GetAccountPolicy(ctx context.Context, accountID string) (*guard.GuardAccountPolicy, error)
	ListAccountPolicies(ctx context.Context) (map[string]*guard.GuardAccountPolicy, error)
	UpsertAccountPolicy(ctx context.Context, p *guard.GuardAccountPolicy) error
}

type CloudService interface {
	ListAccounts(ctx context.Context) ([]cloud.CloudAccount, error)
	ListResources(ctx context.Context, accountID, providerCode, resKind, region, status string) ([]cloud.CloudResource, error)
	GetResource(ctx context.Context, id string) (*cloud.CloudResource, error)
	GetAccount(ctx context.Context, id string) (*cloud.CloudAccount, error)
}

type Engine interface {
	EvaluateOnce(ctx context.Context, isDryRun bool) (*guard.EvaluateResult, error)
}

type Handler struct {
	guardEngine Engine
	store       Store
	cloudSvc    CloudService
	jobEngine   *jobs.Engine
	NowFunc     func() time.Time
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now()
}

func NewHandler(ge Engine, store Store, cloudSvc CloudService, je *jobs.Engine) *Handler {
	return &Handler{
		guardEngine: ge,
		store:       store,
		cloudSvc:    cloudSvc,
		jobEngine:   je,
	}
}

func (h *Handler) RegisterAppRoutes(a *app.App) {
	a.HandleAuthed("GET /api/v1/guard/overview", h.HandleOverview)
	a.HandleAuthed("POST /api/v1/guard/dry-run", h.HandleDryRun)
	a.HandleAuthed("POST /api/v1/guard/evaluate", h.HandleEvaluate)
	a.HandleAuthed("GET /api/v1/guard/cycles", h.HandleListCycles)
	a.HandleAuthed("PUT /api/v1/guard/rules/", h.HandleUpdateRule)
	a.HandleAuthed("PUT /api/v1/guard/accounts/", h.HandleUpdateAccountPolicy)
	a.HandleAuthed("POST /api/v1/guard/instances/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/force-start") {
			h.HandleForceStart(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// HandleOverview handles GET /api/v1/guard/overview.
func (h *Handler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	accounts, err := h.cloudSvc.ListAccounts(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rulesMap, err := h.store.ListRules(ctx)
	if err != nil {
		rulesMap = make(map[string]*guard.GuardRule)
	}

	accountPolicies, err := h.store.ListAccountPolicies(ctx)
	if err != nil {
		accountPolicies = make(map[string]*guard.GuardAccountPolicy)
	}

	now := h.now()
	nowMs := now.UnixMilli()

	var accountOverviews []guard.AccountOverview
	totalInstances := 0
	guardedCount := 0
	actionsEnabledCount := 0

	for _, acc := range accounts {
		resources, err := h.cloudSvc.ListResources(ctx, acc.ID, "", "ecs", "", "")
		if err != nil {
			logx.Warn("guard: list resources failed for overview", "account_id", acc.ID, "err", err)
			continue
		}

		acctPol := accountPolicies[acc.ID]
		if acctPol == nil {
			// 初始化默认账号策略
			acctPol = &guard.GuardAccountPolicy{
				CloudAccountID:  acc.ID,
				IsEnabled:       true,
				ActionsEnabled:  false,
				TrafficAction:   "stop",
				WarnRatio:       0.8,
				ScheduleEnabled: false,
				ScheduleTZ:      "Asia/Shanghai",
				EvalIntervalS:   60,
			}
		}

		var cdtGB *float64
		// 从 config_json 读缓存的 cdt_traffic_bytes
		var cfgData map[string]any
		if acc.ConfigJSON != "" {
			_ = json.Unmarshal([]byte(acc.ConfigJSON), &cfgData)
			if tb, ok := cfgData["cdt_traffic_bytes"].(float64); ok && tb > 0 {
				gb := tb / (1024 * 1024 * 1024)
				cdtGB = &gb
			}
		}

		var instOverviews []guard.InstanceOverview

		for _, res := range resources {
			totalInstances++
			rule := rulesMap[res.ID]
			if rule == nil {
				rule = &guard.GuardRule{
					ID:              ulid.New(),
					CloudResourceID: res.ID,
					IsEnabled:       true,
					ActionsEnabled:  false,
					TrafficAction:   "stop",
					ScheduleTZ:      "Asia/Shanghai",
					InheritAccount:  true,
				}
			}

			eff := guard.ResolveEffectiveRule(rule, acctPol)

			if eff.IsEnabled {
				guardedCount++
				if eff.ActionsEnabled {
					actionsEnabledCount++
				}
			}

			var pubIPList, privIPList []string
			if res.PublicIPs != "" {
				pubIPList = strings.Split(res.PublicIPs, ",")
			}
			if res.PrivateIPs != "" {
				privIPList = strings.Split(res.PrivateIPs, ",")
			}

			io := guard.InstanceOverview{
				ResourceID:         res.ID,
				ResourceName:       res.Name,
				ResourceRef:        res.ResRef,
				Region:             res.Region,
				Status:             res.Status,
				PublicIPs:          pubIPList,
				PrivateIPs:         privIPList,
				BillingInfo:        res.BillingJSON,
				Rule:               rule,
				InheritAccount:     rule.InheritAccount,
				EffectiveLimitGB:   eff.TrafficLimitGB,
				LimitOrigin:        eff.LimitOrigin,
				EffectiveSchedule:  eff.ScheduleEnabled,
				ScheduleOrigin:     eff.ScheduleOrigin,
				EffectiveActions:   eff.ActionsEnabled,
			}

			if eff.ScheduleEnabled && eff.ScheduleStart != nil && eff.ScheduleStop != nil {
				_, _, nextAct, nextTime, err := guard.EvaluateSchedule(*eff.ScheduleStart, *eff.ScheduleStop, eff.ScheduleTZ, now)
				if err == nil {
					io.NextScheduleAction = nextAct
					if nextTime != nil {
						ms := nextTime.UnixMilli()
						io.NextScheduleTimeMs = &ms
					}
				}
			}

			instOverviews = append(instOverviews, io)
		}

		// 账号级 CDT 阈值与使用百分比
		var acctLimit *float64
		if acctPol != nil && acctPol.TrafficLimitGB != nil {
			acctLimit = acctPol.TrafficLimitGB
		}

		usagePct := 0.0
		if cdtGB != nil && acctLimit != nil && *acctLimit > 0 {
			usagePct = (*cdtGB / *acctLimit) * 100.0
		}

		accountOverviews = append(accountOverviews, guard.AccountOverview{
			AccountID:      acc.ID,
			AccountName:    acc.Name,
			ProviderCode:   acc.ProviderCode,
			DefaultRegion:  acc.DefaultRegion,
			AccountSite:    acc.AccountSite,
			CDTUsedGB:      cdtGB,
			TrafficLimitGB: acctLimit,
			UsagePercent:   usagePct,
			Policy:         acctPol,
			Instances:      instOverviews,
		})
	}

	cycles, _, _ := h.store.ListRecentCycles(ctx, 15, 0)

	resp := guard.OverviewResponse{
		Accounts:       accountOverviews,
		RecentCycles:   cycles,
		TotalInstances: totalInstances,
		GuardedCount:   guardedCount,
		ActionsEnabled: actionsEnabledCount,
		CurrentTimeMs:  nowMs,
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleUpdateAccountPolicy handles PUT /api/v1/guard/accounts/{account_id}/policy.
func (h *Handler) HandleUpdateAccountPolicy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/guard/accounts/")
	accountID := strings.TrimSuffix(path, "/policy")
	accountID = strings.Trim(accountID, "/")
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id is required"})
		return
	}

	var req guard.AccountPolicyUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	ctx := r.Context()
	existing, err := h.store.GetAccountPolicy(ctx, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	policy := existing
	if policy == nil {
		policy = &guard.GuardAccountPolicy{
			CloudAccountID:  accountID,
			IsEnabled:       true,
			ActionsEnabled:  false,
			TrafficAction:   "stop",
			WarnRatio:       0.8,
			ScheduleEnabled: false,
			ScheduleTZ:      "Asia/Shanghai",
			EvalIntervalS:   60,
		}
	}

	if req.IsEnabled != nil {
		policy.IsEnabled = *req.IsEnabled
	}
	if req.ActionsEnabled != nil {
		policy.ActionsEnabled = *req.ActionsEnabled
	}
	if req.TrafficLimitGB != nil {
		policy.TrafficLimitGB = req.TrafficLimitGB
	}
	if req.TrafficAction != nil {
		policy.TrafficAction = *req.TrafficAction
	}
	if req.WarnRatio != nil {
		policy.WarnRatio = *req.WarnRatio
	}
	if req.ScheduleEnabled != nil {
		policy.ScheduleEnabled = *req.ScheduleEnabled
	}
	if req.ScheduleStart != nil {
		policy.ScheduleStart = req.ScheduleStart
	}
	if req.ScheduleStop != nil {
		policy.ScheduleStop = req.ScheduleStop
	}
	if req.ScheduleTZ != nil {
		policy.ScheduleTZ = *req.ScheduleTZ
	}
	if req.EvalIntervalS != nil {
		policy.EvalIntervalS = *req.EvalIntervalS
	}

	if err := h.store.UpsertAccountPolicy(ctx, policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, policy)
}

// HandleUpdateRule handles PUT /api/v1/guard/rules/{resource_id}.
func (h *Handler) HandleUpdateRule(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimPrefix(r.URL.Path, "/api/v1/guard/rules/")
	if resourceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource_id is required"})
		return
	}

	var req guard.RuleUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	ctx := r.Context()
	existing, err := h.store.GetRuleByResourceID(ctx, resourceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rule := existing
	if rule == nil {
		rule = &guard.GuardRule{
			ID:              ulid.New(),
			CloudResourceID: resourceID,
			IsEnabled:       true,
			ActionsEnabled:  false,
			TrafficAction:   "stop",
			ScheduleTZ:      "Asia/Shanghai",
			InheritAccount:  true,
		}
	}

	if req.IsEnabled != nil {
		rule.IsEnabled = *req.IsEnabled
	}
	if req.ActionsEnabled != nil {
		rule.ActionsEnabled = *req.ActionsEnabled
	}
	if req.TrafficLimitGB != nil {
		rule.TrafficLimitGB = req.TrafficLimitGB
	}
	if req.TrafficAction != nil {
		rule.TrafficAction = *req.TrafficAction
	}
	if req.ScheduleEnabled != nil {
		rule.ScheduleEnabled = *req.ScheduleEnabled
	}
	if req.ScheduleStart != nil {
		rule.ScheduleStart = req.ScheduleStart
	}
	if req.ScheduleStop != nil {
		rule.ScheduleStop = req.ScheduleStop
	}
	if req.ScheduleTZ != nil {
		rule.ScheduleTZ = *req.ScheduleTZ
	}
	if req.InheritAccount != nil {
		rule.InheritAccount = *req.InheritAccount
	}

	if err := h.store.UpsertRule(ctx, rule); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, rule)
}

// HandleDryRun handles POST /api/v1/guard/dry-run.
func (h *Handler) HandleDryRun(w http.ResponseWriter, r *http.Request) {
	result, err := h.guardEngine.EvaluateOnce(r.Context(), true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleEvaluate handles POST /api/v1/guard/evaluate.
func (h *Handler) HandleEvaluate(w http.ResponseWriter, r *http.Request) {
	result, err := h.guardEngine.EvaluateOnce(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleForceStart handles POST /api/v1/guard/instances/{id}/force-start.
// 验收 11: 手动强制启动 → 有二次确认弹窗，audit_log 里有记录。
func (h *Handler) HandleForceStart(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/guard/instances/")
	resourceID := strings.TrimSuffix(path, "/force-start")
	if resourceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource_id is required"})
		return
	}

	var req guard.ForceStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	if !req.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "force start requires explicit confirmation"})
		return
	}

	ctx := r.Context()
	user := auth.UserFromContext(ctx)
	actor := "user"
	if user != nil {
		actor = user.Username
	}

	res, err := h.cloudSvc.GetResource(ctx, resourceID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
		return
	}

	acc, err := h.cloudSvc.GetAccount(ctx, res.CloudAccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cloud account not found"})
		return
	}

	// 记录 audit_log (验收 11)
	_ = audit.Log(ctx, h.store.DB(), audit.Entry{
		ActorKind:  "user",
		ActorID:    actor,
		Action:     "guard.force_start",
		TargetKind: "cloud_resource",
		TargetID:   res.ID,
		Detail:     fmt.Sprintf("user %s manually force-started instance %s (%s); reason: %s", actor, res.Name, res.ResRef, req.Reason),
		Result:     "submitted",
	})

	jobReq := jobs.SubmitRequest{
		Kind:         guard.JobKindECSStart,
		TargetKind:   "cloud_resource",
		TargetID:     res.ID,
		RejectIfBusy: true,
		Params: map[string]any{
			"account_id":    acc.ID,
			"resource_id":   res.ID,
			"ref":           res.ResRef,
			"region":        res.Region,
			"provider_code": acc.ProviderCode,
			"action":        "start",
			"reason":        "manual force start: " + req.Reason,
		},
		CreatedBy: actor,
	}

	job, err := h.jobEngine.Submit(ctx, jobReq)
	if err != nil {
		if errors.Is(err, jobs.ErrTargetBusy) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "target instance already has an active job"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"message": "force start initiated",
		"job_id":  job.ID,
	})
}

// HandleListCycles handles GET /api/v1/guard/cycles.
func (h *Handler) HandleListCycles(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 1 {
			offset = (v - 1) * limit
		}
	}

	cycles, total, err := h.store.ListRecentCycles(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if cycles == nil {
		cycles = []guard.GuardCycle{}
	}

	page := (offset / limit) + 1
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     cycles,
		"total":     total,
		"page":      page,
		"page_size": limit,
	})
}

func writeJSON(w http.ResponseWriter, code int, val any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(val)
}
