package inventory

import (
	"net/http"

	"dash/internal/app"
	"dash/internal/db"
	"dash/internal/ingest"
)

// InventoryModule 实现 app.Module，装配机器资产与清单维护的全部 API。
type InventoryModule struct {
	db      *db.DB
	nodes   *NodeService
	groups  *GroupService
	tags    *TagService
	facts   *FactsService
	billing *BillingService
	enroll  *EnrollService
}

func NewModule() *InventoryModule {
	return &InventoryModule{}
}

func (m *InventoryModule) Name() string {
	return "inventory"
}

func (m *InventoryModule) Register(a *app.App) error {
	m.Init(a.DB)
	if m.nodes != nil {
		m.nodes.SetLatestGetter(func(nodeID string) (any, bool) {
			if a != nil && a.Ingester != nil {
				if s, ok := a.Ingester.(interface{ Latest() *ingest.LatestCache }); ok {
					return s.Latest().Get(nodeID)
				}
			}
			return nil, false
		})
	}
	m.RegisterAppRoutes(a)
	return nil
}

func (m *InventoryModule) Init(database *db.DB) {
	m.db = database
	m.groups = NewGroupService(database)
	m.tags = NewTagService(database)
	m.facts = NewFactsService(database)
	m.billing = NewBillingService(database)
	m.enroll = NewEnrollService(database)
	m.nodes = NewNodeService(database, m.tags, m.facts, m.billing)
}

func (m *InventoryModule) NodeService() *NodeService       { return m.nodes }
func (m *InventoryModule) GroupService() *GroupService     { return m.groups }
func (m *InventoryModule) TagService() *TagService         { return m.tags }
func (m *InventoryModule) FactsService() *FactsService     { return m.facts }
func (m *InventoryModule) BillingService() *BillingService { return m.billing }
func (m *InventoryModule) EnrollService() *EnrollService   { return m.enroll }

// RegisterAppRoutes 注册所有机器清单相关的 HTTP API 路由到 App（控制台 API 默认走鉴权）。
func (m *InventoryModule) RegisterAppRoutes(a *app.App) {
	// 节点相关路由
	a.HandleAuthed("GET /api/v1/nodes", m.handleNodesList)
	a.HandleAuthed("POST /api/v1/nodes", m.handleNodesCreate)
	a.HandleAuthed("GET /api/v1/nodes/{id}", m.handleNodesGet)
	a.HandleAuthed("PATCH /api/v1/nodes/{id}", m.handleNodesUpdate)
	a.HandleAuthed("DELETE /api/v1/nodes/{id}", m.handleNodesDelete)
	a.HandleAuthed("POST /api/v1/nodes/{id}/token:revoke", m.handleNodesRevokeToken)

	// 节点事实
	a.HandleAuthed("GET /api/v1/nodes/{id}/facts", m.handleFactsGet)
	a.HandleAuthed("PUT /api/v1/nodes/{id}/facts", m.handleFactsPut)

	// 节点计费
	a.HandleAuthed("GET /api/v1/nodes/{id}/billing", m.handleBillingGet)
	a.HandleAuthed("PATCH /api/v1/nodes/{id}/billing", m.handleBillingUpdate)
	a.HandleAuthed("PUT /api/v1/nodes/{id}/billing", m.handleBillingUpdate)
	a.HandleAuthed("DELETE /api/v1/nodes/{id}/billing", m.handleBillingDelete)

	// 标签关联
	a.HandleAuthed("POST /api/v1/nodes/{id}/tags", m.handleReplaceNodeTags)
	a.HandleAuthed("POST /api/v1/nodes/{id}/tags/{tagId}", m.handleAttachNodeTag)
	a.HandleAuthed("DELETE /api/v1/nodes/{id}/tags/{tagId}", m.handleDetachNodeTag)
	a.HandleAuthed("POST /api/v1/nodes/tags:batch", m.handleBatchNodeTags)

	// 分组 CRUD
	a.HandleAuthed("GET /api/v1/node-groups", m.handleGroupsList)
	a.HandleAuthed("POST /api/v1/node-groups", m.handleGroupsCreate)
	a.HandleAuthed("PATCH /api/v1/node-groups/{id}", m.handleGroupsUpdate)
	a.HandleAuthed("DELETE /api/v1/node-groups/{id}", m.handleGroupsDelete)

	// 标签 CRUD
	a.HandleAuthed("GET /api/v1/tags", m.handleTagsList)
	a.HandleAuthed("POST /api/v1/tags", m.handleTagsCreate)
	a.HandleAuthed("PATCH /api/v1/tags/{id}", m.handleTagsUpdate)
	a.HandleAuthed("DELETE /api/v1/tags/{id}", m.handleTagsDelete)

	// 注册令牌
	a.HandleAuthed("GET /api/v1/enroll-tokens", m.handleEnrollList)
	a.HandleAuthed("POST /api/v1/enroll-tokens", m.handleEnrollCreate)
	a.HandleAuthed("DELETE /api/v1/enroll-tokens/{id}", m.handleEnrollDelete)
}

// RegisterRoutes 注册所有机器清单相关的 HTTP API 路由至原生 ServeMux（用于内部测试）。
func (m *InventoryModule) RegisterRoutes(mux *http.ServeMux) {
	// 节点相关路由
	mux.HandleFunc("GET /api/v1/nodes", m.handleNodesList)
	mux.HandleFunc("POST /api/v1/nodes", m.handleNodesCreate)
	mux.HandleFunc("GET /api/v1/nodes/{id}", m.handleNodesGet)
	mux.HandleFunc("PATCH /api/v1/nodes/{id}", m.handleNodesUpdate)
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", m.handleNodesDelete)
	mux.HandleFunc("POST /api/v1/nodes/{id}/token:revoke", m.handleNodesRevokeToken)

	// 节点事实
	mux.HandleFunc("GET /api/v1/nodes/{id}/facts", m.handleFactsGet)
	mux.HandleFunc("PUT /api/v1/nodes/{id}/facts", m.handleFactsPut)

	// 节点计费
	mux.HandleFunc("GET /api/v1/nodes/{id}/billing", m.handleBillingGet)
	mux.HandleFunc("PATCH /api/v1/nodes/{id}/billing", m.handleBillingUpdate)
	mux.HandleFunc("PUT /api/v1/nodes/{id}/billing", m.handleBillingUpdate)
	mux.HandleFunc("DELETE /api/v1/nodes/{id}/billing", m.handleBillingDelete)

	// 标签关联
	mux.HandleFunc("POST /api/v1/nodes/{id}/tags", m.handleReplaceNodeTags)
	mux.HandleFunc("POST /api/v1/nodes/{id}/tags/{tagId}", m.handleAttachNodeTag)
	mux.HandleFunc("DELETE /api/v1/nodes/{id}/tags/{tagId}", m.handleDetachNodeTag)
	mux.HandleFunc("POST /api/v1/nodes/tags:batch", m.handleBatchNodeTags)

	// 分组 CRUD
	mux.HandleFunc("GET /api/v1/node-groups", m.handleGroupsList)
	mux.HandleFunc("POST /api/v1/node-groups", m.handleGroupsCreate)
	mux.HandleFunc("PATCH /api/v1/node-groups/{id}", m.handleGroupsUpdate)
	mux.HandleFunc("DELETE /api/v1/node-groups/{id}", m.handleGroupsDelete)

	// 标签 CRUD
	mux.HandleFunc("GET /api/v1/tags", m.handleTagsList)
	mux.HandleFunc("POST /api/v1/tags", m.handleTagsCreate)
	mux.HandleFunc("PATCH /api/v1/tags/{id}", m.handleTagsUpdate)
	mux.HandleFunc("DELETE /api/v1/tags/{id}", m.handleTagsDelete)

	// 注册令牌
	mux.HandleFunc("GET /api/v1/enroll-tokens", m.handleEnrollList)
	mux.HandleFunc("POST /api/v1/enroll-tokens", m.handleEnrollCreate)
	mux.HandleFunc("DELETE /api/v1/enroll-tokens/{id}", m.handleEnrollDelete)
}

func (m *InventoryModule) handleNodesList(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleList(w, r)
}

func (m *InventoryModule) handleNodesCreate(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleCreate(w, r)
}

func (m *InventoryModule) handleNodesGet(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleGet(w, r)
}

func (m *InventoryModule) handleNodesUpdate(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleUpdate(w, r)
}

func (m *InventoryModule) handleNodesDelete(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleDelete(w, r)
}

func (m *InventoryModule) handleNodesRevokeToken(w http.ResponseWriter, r *http.Request) {
	if m.nodes == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.nodes.HandleRevokeToken(w, r)
}

func (m *InventoryModule) handleFactsGet(w http.ResponseWriter, r *http.Request) {
	if m.facts == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.facts.HandleGet(w, r)
}

func (m *InventoryModule) handleFactsPut(w http.ResponseWriter, r *http.Request) {
	if m.facts == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.facts.HandlePut(w, r)
}

func (m *InventoryModule) handleBillingGet(w http.ResponseWriter, r *http.Request) {
	if m.billing == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.billing.HandleGet(w, r)
}

func (m *InventoryModule) handleBillingUpdate(w http.ResponseWriter, r *http.Request) {
	if m.billing == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.billing.HandleUpdate(w, r)
}

func (m *InventoryModule) handleBillingDelete(w http.ResponseWriter, r *http.Request) {
	if m.billing == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.billing.HandleDelete(w, r)
}

func (m *InventoryModule) handleReplaceNodeTags(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleReplaceNodeTags(w, r)
}

func (m *InventoryModule) handleAttachNodeTag(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleAttachNodeTag(w, r)
}

func (m *InventoryModule) handleDetachNodeTag(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleDetachNodeTag(w, r)
}

func (m *InventoryModule) handleBatchNodeTags(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleBatchNodeTags(w, r)
}

func (m *InventoryModule) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	if m.groups == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.groups.HandleList(w, r)
}

func (m *InventoryModule) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	if m.groups == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.groups.HandleCreate(w, r)
}

func (m *InventoryModule) handleGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	if m.groups == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.groups.HandleUpdate(w, r)
}

func (m *InventoryModule) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	if m.groups == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.groups.HandleDelete(w, r)
}

func (m *InventoryModule) handleTagsList(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleList(w, r)
}

func (m *InventoryModule) handleTagsCreate(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleCreate(w, r)
}

func (m *InventoryModule) handleTagsUpdate(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleUpdate(w, r)
}

func (m *InventoryModule) handleTagsDelete(w http.ResponseWriter, r *http.Request) {
	if m.tags == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.tags.HandleDelete(w, r)
}

func (m *InventoryModule) handleEnrollList(w http.ResponseWriter, r *http.Request) {
	if m.enroll == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.enroll.HandleList(w, r)
}

func (m *InventoryModule) handleEnrollCreate(w http.ResponseWriter, r *http.Request) {
	if m.enroll == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.enroll.HandleCreate(w, r)
}

func (m *InventoryModule) handleEnrollDelete(w http.ResponseWriter, r *http.Request) {
	if m.enroll == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未初始化", nil)
		return
	}
	m.enroll.HandleDelete(w, r)
}
