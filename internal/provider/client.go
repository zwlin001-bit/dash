package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ProviderClient interface defines interactions with any cloud provider plugin.
type ProviderClient interface {
	Describe(ctx context.Context) (*ProviderDescription, error)
	Healthcheck(ctx context.Context, cred map[string]string, region string) (*HealthcheckResult, error)
	ListRegions(ctx context.Context, cred map[string]string) ([]Region, error)
	ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]NormalizedResource, error)
	GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*NormalizedResource, error)
	Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]NormalizedResource, error)
	GetCDTTraffic(ctx context.Context, cred map[string]string) (*CDTTrafficResult, error)
	Action(ctx context.Context, req ActionParams) (*ActionResponse, error)
	PollJob(ctx context.Context, jobHandle string) (*PollJobResponse, error)
	ListMetrics(ctx context.Context, params MetricListParams) (*MetricListResult, error)
	ListBills(ctx context.Context, cred map[string]string, period, accountSite string) (*BillListResult, error)
	Close() error
}

// RPCClient communicates with a provider process over a Unix domain socket.
type RPCClient struct {
	socketPath string
	dialer     func(ctx context.Context) (net.Conn, error)
	mu         sync.Mutex
	conn       net.Conn
	reader     *bufio.Reader
	seq        uint64
}

// NewRPCClient connects to the given Unix socket path.
func NewRPCClient(socketPath string) *RPCClient {
	return &RPCClient{
		socketPath: socketPath,
		dialer: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
}

// NewRPCClientWithDialer allows supplying a custom dialer (e.g. for testing with in-memory pipes).
func NewRPCClientWithDialer(dialer func(ctx context.Context) (net.Conn, error)) *RPCClient {
	return &RPCClient{
		dialer: dialer,
	}
}

func (c *RPCClient) ensureConn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	conn, err := c.dialer(ctx)
	if err != nil {
		return fmt.Errorf("provider: dial unix socket %q failed: %w", c.socketPath, err)
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	return nil
}

func (c *RPCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.reader = nil
		return err
	}
	return nil
}

func (c *RPCClient) call(ctx context.Context, method string, params any, result any) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("%d", atomic.AddUint64(&c.seq, 1))

	var rawParams json.RawMessage
	if params != nil {
		pBytes, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("provider: marshal params: %w", err)
		}
		rawParams = pBytes
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("provider: marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
	} else {
		_ = c.conn.SetDeadline(time.Now().Add(60 * time.Second))
	}
	defer func() {
		if c.conn != nil {
			_ = c.conn.SetDeadline(time.Time{})
		}
	}()

	if _, err := c.conn.Write(reqBytes); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.reader = nil
		return fmt.Errorf("provider: write request failed: %w", err)
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.reader = nil
		return fmt.Errorf("provider: read response failed: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("provider: unmarshal response failed: %w (raw: %s)", err, string(line))
	}

	if resp.Error != nil {
		return resp.Error
	}

	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("provider: unmarshal result into %T failed: %w", result, err)
		}
	}

	return nil
}

func (c *RPCClient) Describe(ctx context.Context) (*ProviderDescription, error) {
	var res ProviderDescription
	if err := c.call(ctx, "provider.describe", nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) Healthcheck(ctx context.Context, cred map[string]string, region string) (*HealthcheckResult, error) {
	var res HealthcheckResult
	params := HealthcheckParams{Credential: cred, Region: region}
	if err := c.call(ctx, "provider.healthcheck", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) ListRegions(ctx context.Context, cred map[string]string) ([]Region, error) {
	var res []Region
	params := ListRegionsParams{Credential: cred}
	if err := c.call(ctx, "provider.regions", params, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = []Region{}
	}
	return res, nil
}

func (c *RPCClient) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]NormalizedResource, error) {
	var res []NormalizedResource
	params := ListResourcesParams{Credential: cred, Region: region, Kind: kind, AccountSite: accountSite}
	if err := c.call(ctx, "resource.list", params, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = []NormalizedResource{}
	}
	return res, nil
}

func (c *RPCClient) GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*NormalizedResource, error) {
	var res NormalizedResource
	params := GetResourceParams{Credential: cred, Region: region, Kind: kind, Ref: ref}
	if err := c.call(ctx, "resource.get", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]NormalizedResource, error) {
	var res []NormalizedResource
	params := DiscoverParams{Credential: cred, Regions: regions, AccountSite: accountSite}
	if err := c.call(ctx, "provider.discover", params, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = []NormalizedResource{}
	}
	return res, nil
}

func (c *RPCClient) GetCDTTraffic(ctx context.Context, cred map[string]string) (*CDTTrafficResult, error) {
	var res CDTTrafficResult
	params := TrafficParams{Credential: cred}
	if err := c.call(ctx, "traffic.get", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) Action(ctx context.Context, req ActionParams) (*ActionResponse, error) {
	var res ActionResponse
	if err := c.call(ctx, "resource.action", req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) PollJob(ctx context.Context, jobHandle string) (*PollJobResponse, error) {
	var res PollJobResponse
	params := PollJobParams{JobHandle: jobHandle}
	if err := c.call(ctx, "job.poll", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) ListMetrics(ctx context.Context, params MetricListParams) (*MetricListResult, error) {
	var res MetricListResult
	if err := c.call(ctx, "metric.list", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *RPCClient) ListBills(ctx context.Context, cred map[string]string, period, accountSite string) (*BillListResult, error) {
	var res BillListResult
	params := BillListParams{Credential: cred, Period: period, AccountSite: accountSite}
	if err := c.call(ctx, "bill.list", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

var _ ProviderClient = (*RPCClient)(nil)

// MockClient is an in-memory client implementation useful for testing
type MockClient struct {
	DescribeFunc      func(ctx context.Context) (*ProviderDescription, error)
	HealthcheckFunc   func(ctx context.Context, cred map[string]string, region string) (*HealthcheckResult, error)
	ListRegionsFunc   func(ctx context.Context, cred map[string]string) ([]Region, error)
	ListResourcesFunc func(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]NormalizedResource, error)
	GetResourceFunc   func(ctx context.Context, cred map[string]string, region, kind, ref string) (*NormalizedResource, error)
	DiscoverFunc      func(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]NormalizedResource, error)
	GetCDTTrafficFunc func(ctx context.Context, cred map[string]string) (*CDTTrafficResult, error)
	ActionFunc        func(ctx context.Context, req ActionParams) (*ActionResponse, error)
	PollJobFunc       func(ctx context.Context, jobHandle string) (*PollJobResponse, error)
	ListMetricsFunc   func(ctx context.Context, params MetricListParams) (*MetricListResult, error)
	ListBillsFunc     func(ctx context.Context, cred map[string]string, period, accountSite string) (*BillListResult, error)
}

func (m *MockClient) Describe(ctx context.Context) (*ProviderDescription, error) {
	if m.DescribeFunc != nil {
		return m.DescribeFunc(ctx)
	}
	return &ProviderDescription{ProviderCode: "aliyun", DisplayName: "阿里云"}, nil
}

func (m *MockClient) Healthcheck(ctx context.Context, cred map[string]string, region string) (*HealthcheckResult, error) {
	if m.HealthcheckFunc != nil {
		return m.HealthcheckFunc(ctx, cred, region)
	}
	return &HealthcheckResult{OK: true}, nil
}

func (m *MockClient) ListRegions(ctx context.Context, cred map[string]string) ([]Region, error) {
	if m.ListRegionsFunc != nil {
		return m.ListRegionsFunc(ctx, cred)
	}
	return []Region{}, nil
}

func (m *MockClient) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]NormalizedResource, error) {
	if m.ListResourcesFunc != nil {
		return m.ListResourcesFunc(ctx, cred, region, kind, accountSite)
	}
	return nil, nil
}

func (m *MockClient) GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*NormalizedResource, error) {
	if m.GetResourceFunc != nil {
		return m.GetResourceFunc(ctx, cred, region, kind, ref)
	}
	return nil, errors.New("not found")
}

func (m *MockClient) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]NormalizedResource, error) {
	if m.DiscoverFunc != nil {
		return m.DiscoverFunc(ctx, cred, regions, accountSite)
	}
	return nil, nil
}

func (m *MockClient) GetCDTTraffic(ctx context.Context, cred map[string]string) (*CDTTrafficResult, error) {
	if m.GetCDTTrafficFunc != nil {
		return m.GetCDTTrafficFunc(ctx, cred)
	}
	return &CDTTrafficResult{TrafficBytes: 0}, nil
}

func (m *MockClient) Action(ctx context.Context, req ActionParams) (*ActionResponse, error) {
	if m.ActionFunc != nil {
		return m.ActionFunc(ctx, req)
	}
	return &ActionResponse{JobHandle: "job-1", Status: "succeeded"}, nil
}

func (m *MockClient) PollJob(ctx context.Context, jobHandle string) (*PollJobResponse, error) {
	if m.PollJobFunc != nil {
		return m.PollJobFunc(ctx, jobHandle)
	}
	return &PollJobResponse{JobHandle: jobHandle, Status: "succeeded"}, nil
}

func (m *MockClient) ListMetrics(ctx context.Context, params MetricListParams) (*MetricListResult, error) {
	if m.ListMetricsFunc != nil {
		return m.ListMetricsFunc(ctx, params)
	}
	return &MetricListResult{ResRef: params.ResRef, MetricCode: params.MetricCode}, nil
}

func (m *MockClient) ListBills(ctx context.Context, cred map[string]string, period, accountSite string) (*BillListResult, error) {
	if m.ListBillsFunc != nil {
		return m.ListBillsFunc(ctx, cred, period, accountSite)
	}
	return &BillListResult{Period: period, Currency: "CNY", Items: []BillItem{}}, nil
}

func (m *MockClient) Close() error {
	return nil
}

var _ ProviderClient = (*MockClient)(nil)
