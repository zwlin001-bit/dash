package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/responses"

	"dash/internal/logx"
	"dash/internal/provider"
)

// DefaultBackoffs are the retry backoffs specified in P2-02 (1s, 4s, 15s).
var DefaultBackoffs = []time.Duration{1 * time.Second, 4 * time.Second, 15 * time.Second}

// Provider implements the Aliyun cloud provider using CommonRequest only.
type Provider struct {
	mu           sync.Mutex
	cdtCache     map[string]*cachedCDT // key: ak_id
	cacheTTL     time.Duration
	retryDelays  []time.Duration
	clientHook   func(region, ak, sk string) (*sdk.Client, error)
	customDomain map[string]string // for testing redirecting domains to mock server
}

type cachedCDT struct {
	result    *provider.CDTTrafficResult
	fetchedAt time.Time
}

// Option configures Provider
type Option func(*Provider)

func WithRetryDelays(delays []time.Duration) Option {
	return func(p *Provider) {
		p.retryDelays = delays
	}
}

func WithClientHook(hook func(region, ak, sk string) (*sdk.Client, error)) Option {
	return func(p *Provider) {
		p.clientHook = hook
	}
}

func WithCustomDomain(api, domain string) Option {
	return func(p *Provider) {
		if p.customDomain == nil {
			p.customDomain = make(map[string]string)
		}
		p.customDomain[api] = domain
	}
}

func NewProvider(opts ...Option) *Provider {
	p := &Provider{
		cdtCache:    make(map[string]*cachedCDT),
		cacheTTL:    60 * time.Second,
		retryDelays: DefaultBackoffs,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Provider) Describe() *provider.ProviderDescription {
	return &provider.ProviderDescription{
		ProviderCode: "aliyun",
		DisplayName:  "阿里云",
		SupportedKinds: []string{
			"instance",
		},
		CredentialFields: []string{
			"access_key_id",
			"access_key_secret",
		},
		Capabilities: []string{
			"discover",
			"action_start",
			"action_stop",
			"cdt_traffic",
			"billing",
			"metric_list",
		},
	}
}

func (p *Provider) getSDKClient(region, ak, sk string) (*sdk.Client, error) {
	if p.clientHook != nil {
		return p.clientHook(region, ak, sk)
	}
	if region == "" {
		region = "cn-hangzhou"
	}
	client, err := sdk.NewClientWithAccessKey(region, ak, sk)
	if err != nil {
		return nil, fmt.Errorf("aliyun: create sdk client failed: %w", err)
	}
	client.SetConnectTimeout(10 * time.Second)
	client.SetReadTimeout(15 * time.Second)
	return client, nil
}

// isRetryableError implements the retry policy:
// Network errors (timeout, connection reset, 5xx) retry 3 times.
// Auth errors, parameter errors, client 4xx (except rate limit) do NOT retry.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var sdkErr sdkerrors.Error
	if errors.As(err, &sdkErr) {
		status := sdkErr.HttpStatus()
		errCode := strings.ToLower(sdkErr.ErrorCode())

		// Throttling / Rate limits are retryable
		if status == http.StatusTooManyRequests || strings.Contains(errCode, "throttling") || strings.Contains(errCode, "requestlimitexceeded") {
			return true
		}

		// 5xx server errors are retryable
		if status >= 500 {
			return true
		}

		// 4xx client errors (auth, bad param, etc.) are NOT retryable
		if status >= 400 && status < 500 {
			return false
		}
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "connection reset") || strings.Contains(errStr, "eof") {
		return true
	}

	return false
}

// doWithRetry executes a common request with 3 retries on network/5xx errors
func (p *Provider) doWithRetry(ctx context.Context, client *sdk.Client, req *requests.CommonRequest) (*responses.CommonResponse, error) {
	var lastErr error

	attempts := len(p.retryDelays) + 1
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := client.ProcessCommonRequest(req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !isRetryableError(err) {
			// Do not retry client auth/param errors
			return nil, err
		}

		if i < len(p.retryDelays) {
			delay := p.retryDelays[i]
			logx.Warn("aliyun api call failed, retrying", "attempt", i+1, "delay", delay, "err", logx.Redact(err.Error()))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return nil, fmt.Errorf("aliyun request failed after %d attempts: %w", attempts, lastErr)
}

// Healthcheck verifies AK/SK credentials by checking connectivity
func (p *Provider) Healthcheck(ctx context.Context, cred map[string]string, region string) (*provider.HealthcheckResult, error) {
	ak := cred["access_key_id"]
	sk := cred["access_key_secret"]
	if ak == "" || sk == "" {
		return &provider.HealthcheckResult{OK: false, Message: "missing access_key_id or access_key_secret"}, nil
	}
	if region == "" {
		region = "cn-hangzhou"
	}

	client, err := p.getSDKClient(region, ak, sk)
	if err != nil {
		return &provider.HealthcheckResult{OK: false, Message: err.Error()}, nil
	}

	req := requests.NewCommonRequest()
	req.Method = "POST"
	if d, ok := p.customDomain["DescribeRegions"]; ok {
		req.Domain = d
	} else {
		req.Domain = fmt.Sprintf("ecs.%s.aliyuncs.com", region)
	}
	req.Version = "2014-05-26"
	req.ApiName = "DescribeRegions"
	req.Product = "Ecs"

	_, err = p.doWithRetry(ctx, client, req)
	if err != nil {
		return &provider.HealthcheckResult{OK: false, Message: logx.Redact(err.Error())}, nil
	}

	return &provider.HealthcheckResult{OK: true, Message: "OK"}, nil
}

// GetCDTTraffic fetches account-level CDT traffic bytes.
// Facts: CDT traffic is account-level. Sum across all TrafficDetails[].Traffic. Cached per account.
func (p *Provider) GetCDTTraffic(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
	ak := cred["access_key_id"]
	sk := cred["access_key_secret"]
	if ak == "" || sk == "" {
		return nil, errors.New("aliyun: missing credentials")
	}

	p.mu.Lock()
	if cached, ok := p.cdtCache[ak]; ok && time.Since(cached.fetchedAt) < p.cacheTTL {
		res := cached.result
		p.mu.Unlock()
		return res, nil
	}
	p.mu.Unlock()

	client, err := p.getSDKClient("cn-hangzhou", ak, sk)
	if err != nil {
		return nil, err
	}

	req := requests.NewCommonRequest()
	req.Method = "POST"
	if d, ok := p.customDomain["ListCdtInternetTraffic"]; ok {
		req.Domain = d
	} else {
		req.Domain = "cdt.aliyuncs.com"
	}
	req.Version = "2021-08-13"
	req.ApiName = "ListCdtInternetTraffic"
	req.Product = "CDT"

	resp, err := p.doWithRetry(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("aliyun cdt traffic error: %w", err)
	}

	result, err := parseCDTResponse(resp.GetHttpContentBytes())
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.cdtCache[ak] = &cachedCDT{
		result:    result,
		fetchedAt: time.Now(),
	}
	p.mu.Unlock()

	return result, nil
}

func parseCDTResponse(body []byte) (*provider.CDTTrafficResult, error) {
	var raw struct {
		TrafficDetails struct {
			TrafficDetail []struct {
				Traffic      any    `json:"Traffic"`
				ProductType  string `json:"ProductType"`
				BusinessTime string `json:"BusinessTime"`
			} `json:"TrafficDetail"`
		} `json:"TrafficDetails"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("aliyun: parse cdt response: %w", err)
	}

	var totalBytes int64
	var details []provider.CDTTrafficDetail

	for _, d := range raw.TrafficDetails.TrafficDetail {
		var bytesVal int64
		switch v := d.Traffic.(type) {
		case float64:
			bytesVal = int64(v)
		case int64:
			bytesVal = v
		case string:
			bytesVal, _ = strconv.ParseInt(v, 10, 64)
		}

		totalBytes += bytesVal
		details = append(details, provider.CDTTrafficDetail{
			Traffic:     bytesVal,
			ProductType: d.ProductType,
		})
	}

	return &provider.CDTTrafficResult{
		TrafficBytes: totalBytes,
		Details:      details,
	}, nil
}

// DescribeInstancesGroup queries up to 100 ECS instances in a specific region.
func (p *Provider) DescribeInstancesGroup(ctx context.Context, ak, sk, region string) ([]provider.NormalizedResource, error) {
	client, err := p.getSDKClient(region, ak, sk)
	if err != nil {
		return nil, err
	}

	req := requests.NewCommonRequest()
	req.Method = "POST"
	if d, ok := p.customDomain["DescribeInstances"]; ok {
		req.Domain = d
	} else {
		req.Domain = fmt.Sprintf("ecs.%s.aliyuncs.com", region)
	}
	req.Version = "2014-05-26"
	req.ApiName = "DescribeInstances"
	req.Product = "Ecs"
	req.QueryParams["RegionId"] = region
	req.QueryParams["PageSize"] = "100"

	resp, err := p.doWithRetry(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("aliyun describe instances failed for region %s: %w", region, err)
	}

	return parseDescribeInstances(resp.GetHttpContentBytes(), region)
}

func parseDescribeInstances(body []byte, defaultRegion string) ([]provider.NormalizedResource, error) {
	var raw struct {
		Instances struct {
			Instance []struct {
				InstanceId              string `json:"InstanceId"`
				InstanceName            string `json:"InstanceName"`
				Status                  string `json:"Status"`
				RegionId                string `json:"RegionId"`
				Cpu                     int    `json:"Cpu"`
				Memory                  int    `json:"Memory"`
				InstanceChargeType      string `json:"InstanceChargeType"`
				InternetMaxBandwidthOut int    `json:"InternetMaxBandwidthOut"`
				InternetMaxBandwidthIn  int    `json:"InternetMaxBandwidthIn"`
				InstanceType            string `json:"InstanceType"`
				OSType                  string `json:"OSType"`
				OSName                  string `json:"OSName"`
				CreationTime            string `json:"CreationTime"`
				ExpiredTime             string `json:"ExpiredTime"`
				PublicIpAddress         struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"PublicIpAddress"`
				InnerIpAddress struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"InnerIpAddress"`
				VpcAttributes struct {
					PrivateIpAddress struct {
						IpAddress []string `json:"IpAddress"`
					} `json:"PrivateIpAddress"`
				} `json:"VpcAttributes"`
				EipAddress struct {
					IpAddress string `json:"IpAddress"`
				} `json:"EipAddress"`
			} `json:"Instance"`
		} `json:"Instances"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("aliyun: unmarshal describe instances response: %w", err)
	}

	var res []provider.NormalizedResource
	for _, inst := range raw.Instances.Instance {
		region := inst.RegionId
		if region == "" {
			region = defaultRegion
		}

		var publicIPs []string
		publicIPs = append(publicIPs, inst.PublicIpAddress.IpAddress...)
		if inst.EipAddress.IpAddress != "" {
			publicIPs = append(publicIPs, inst.EipAddress.IpAddress)
		}

		var privateIPs []string
		privateIPs = append(privateIPs, inst.VpcAttributes.PrivateIpAddress.IpAddress...)
		privateIPs = append(privateIPs, inst.InnerIpAddress.IpAddress...)

		// Normalized status: Running -> running, Stopped -> stopped
		status := strings.ToLower(inst.Status)

		// Expiration time parsing
		var expiresAtMs int64
		if inst.ExpiredTime != "" {
			if t, err := time.Parse(time.RFC3339, inst.ExpiredTime); err == nil {
				expiresAtMs = t.UnixMilli()
			}
		}

		attrs := map[string]any{
			"instance_charge_type":        inst.InstanceChargeType,
			"internet_max_bandwidth_out": inst.InternetMaxBandwidthOut,
			"internet_max_bandwidth_in":  inst.InternetMaxBandwidthIn,
			"instance_type":               inst.InstanceType,
			"os_type":                     inst.OSType,
			"os_name":                     inst.OSName,
			"creation_time":               inst.CreationTime,
		}

		res = append(res, provider.NormalizedResource{
			ProviderCode: "aliyun",
			Kind:         "instance",
			Ref:          inst.InstanceId,
			Name:         inst.InstanceName,
			Region:       region,
			Status:       status,
			PublicIPs:    publicIPs,
			PrivateIPs:   privateIPs,
			Specs: provider.ResourceSpecs{
				VCPU:  inst.Cpu,
				MemMB: inst.Memory,
			},
			Billing: provider.ResourceBilling{
				ExpiresAtMs: expiresAtMs,
			},
			Attrs: attrs,
		})
	}

	return res, nil
}

// DescribeInstanceBill queries bill information for instances.
// Rule: BSS failure isolation - BSS failures MUST NOT affect CDT or ECS.
func (p *Provider) DescribeInstanceBill(ctx context.Context, ak, sk, accountSite string) (map[string]float64, error) {
	client, err := p.getSDKClient("cn-hangzhou", ak, sk)
	if err != nil {
		return nil, err
	}

	req := requests.NewCommonRequest()
	req.Method = "POST"
	if d, ok := p.customDomain["DescribeInstanceBill"]; ok {
		req.Domain = d
	} else if accountSite == "international" {
		req.Domain = "business.ap-southeast-1.aliyuncs.com"
	} else {
		req.Domain = "business.aliyuncs.com"
	}
	req.Version = "2017-12-14"
	req.ApiName = "DescribeInstanceBill"
	req.Product = "BssOpenApi"

	now := time.Now()
	billingCycle := fmt.Sprintf("%04d-%02d", now.Year(), int(now.Month()))
	req.QueryParams["BillingCycle"] = billingCycle
	req.QueryParams["ProductCode"] = "ecs"

	resp, err := p.doWithRetry(ctx, client, req)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Data struct {
			Items struct {
				Item []struct {
					InstanceID string  `json:"InstanceID"`
					PretaxAmount float64 `json:"PretaxAmount"`
				} `json:"Item"`
			} `json:"Items"`
		} `json:"Data"`
	}

	if err := json.Unmarshal(resp.GetHttpContentBytes(), &raw); err != nil {
		return nil, err
	}

	prices := make(map[string]float64)
	for _, item := range raw.Data.Items.Item {
		if item.InstanceID != "" {
			prices[item.InstanceID] = item.PretaxAmount
		}
	}
	return prices, nil
}

// ListResources fetches instances in a specific region, enriching with bill information if possible.
// BSS errors are gracefully isolated into BillError.
func (p *Provider) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]provider.NormalizedResource, error) {
	ak := cred["access_key_id"]
	sk := cred["access_key_secret"]
	if ak == "" || sk == "" {
		return nil, errors.New("aliyun: missing credentials")
	}
	if region == "" {
		region = "cn-hangzhou"
	}

	instances, err := p.DescribeInstancesGroup(ctx, ak, sk, region)
	if err != nil {
		return nil, err
	}

	// Try fetching bills with failure isolation
	prices, billErr := p.DescribeInstanceBill(ctx, ak, sk, accountSite)
	billErrStr := ""
	if billErr != nil {
		billErrStr = billErr.Error()
	}

	for i := range instances {
		if billErrStr != "" {
			instances[i].BillError = billErrStr
		} else if price, ok := prices[instances[i].Ref]; ok {
			instances[i].Billing.Price = price
		}
	}

	return instances, nil
}

// Discover scans multiple regions for all instances under the account.
// DescribeInstances is grouped by (credential, region) — exactly 1 call per region.
// CDT is called at most once per account.
func (p *Provider) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
	ak := cred["access_key_id"]
	sk := cred["access_key_secret"]
	if ak == "" || sk == "" {
		return nil, errors.New("aliyun: missing credentials")
	}

	if len(regions) == 0 {
		regions = []string{
			"cn-hangzhou", "cn-shanghai", "cn-beijing", "cn-shenzhen",
			"cn-hongkong", "ap-southeast-1", "ap-northeast-1", "us-west-1",
		}
	}

	// Optional BSS bill query with failure isolation
	bills, billErr := p.DescribeInstanceBill(ctx, ak, sk, accountSite)
	var billErrStr string
	if billErr != nil {
		billErrStr = billErr.Error()
	}

	var allResources []provider.NormalizedResource
	for _, region := range regions {
		insts, err := p.DescribeInstancesGroup(ctx, ak, sk, region)
		if err != nil {
			logx.Warn("discover region failed, skipping", "region", region, "err", logx.Redact(err.Error()))
			continue
		}
		for i := range insts {
			if billErrStr != "" {
				insts[i].BillError = billErrStr
			} else if price, ok := bills[insts[i].Ref]; ok {
				insts[i].Billing.Price = price
			}
		}
		allResources = append(allResources, insts...)
	}

	if allResources == nil {
		allResources = []provider.NormalizedResource{}
	}
	return allResources, nil
}

// Action performs start or stop on an ECS instance asynchronously.
func (p *Provider) Action(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
	ak := req.Credential["access_key_id"]
	sk := req.Credential["access_key_secret"]
	if ak == "" || sk == "" {
		return nil, errors.New("aliyun: missing credentials")
	}
	if req.Region == "" || req.Ref == "" {
		return nil, errors.New("aliyun: region and ref are required")
	}

	client, err := p.getSDKClient(req.Region, ak, sk)
	if err != nil {
		return nil, err
	}

	apiName := ""
	switch req.Action {
	case "start":
		apiName = "StartInstance"
	case "stop":
		apiName = "StopInstance"
	default:
		return nil, fmt.Errorf("aliyun: unsupported action %q", req.Action)
	}

	reqObj := requests.NewCommonRequest()
	reqObj.Method = "POST"
	if d, ok := p.customDomain[apiName]; ok {
		reqObj.Domain = d
	} else {
		reqObj.Domain = fmt.Sprintf("ecs.%s.aliyuncs.com", req.Region)
	}
	reqObj.Version = "2014-05-26"
	reqObj.ApiName = apiName
	reqObj.Product = "Ecs"
	reqObj.QueryParams["RegionId"] = req.Region
	reqObj.QueryParams["InstanceId"] = req.Ref

	_, err = p.doWithRetry(ctx, client, reqObj)
	if err != nil {
		return nil, fmt.Errorf("aliyun %s failed for %s: %w", apiName, req.Ref, err)
	}

	handle := fmt.Sprintf("aliyun-%s-%s-%d", req.Action, req.Ref, time.Now().UnixMilli())
	return &provider.ActionResponse{
		JobHandle: handle,
		Status:    "succeeded",
		Message:   fmt.Sprintf("%s initiated for instance %s", apiName, req.Ref),
	}, nil
}

// ListMetrics fetches metric time-series points from Aliyun.
// Translates dash unified metric_code into provider metric names:
// - _account + traffic_month_up -> CDT internet traffic
// - instance + cpu_pct -> CMS CPUUtilization
// - instance + net_up_bps -> CMS InternetOutRate (bits/s -> bytes/s)
// - instance + net_down_bps -> CMS InternetInRate (bits/s -> bytes/s)
// - instance + mem_used -> CMS memory_used
func (p *Provider) ListMetrics(ctx context.Context, params provider.MetricListParams) (*provider.MetricListResult, error) {
	ak := params.Credential["access_key_id"]
	sk := params.Credential["access_key_secret"]
	if ak == "" || sk == "" {
		return nil, errors.New("aliyun: missing credentials")
	}

	// 1. Account-level CDT Traffic
	if params.ResRef == "_account" && params.MetricCode == "traffic_month_up" {
		cdtRes, err := p.GetCDTTraffic(ctx, params.Credential)
		if err != nil {
			return nil, err
		}
		ts := time.Now().UnixMilli()
		if params.EndTimeMs > 0 {
			ts = params.EndTimeMs
		}
		return &provider.MetricListResult{
			ResRef:     params.ResRef,
			MetricCode: params.MetricCode,
			Points: []provider.MetricPoint{
				{TsMs: ts, Value: float64(cdtRes.TrafficBytes)},
			},
		}, nil
	}

	// 2. CloudMonitor (CMS) ECS Metrics
	var cmsMetricName string
	isRateMetric := false
	switch params.MetricCode {
	case "cpu_pct":
		cmsMetricName = "CPUUtilization"
	case "net_up_bps":
		cmsMetricName = "InternetOutRate"
		isRateMetric = true
	case "net_down_bps":
		cmsMetricName = "InternetInRate"
		isRateMetric = true
	case "mem_used":
		cmsMetricName = "memory_used"
	default:
		return nil, fmt.Errorf("aliyun: unsupported metric code %q", params.MetricCode)
	}

	region := params.Region
	if region == "" {
		region = "cn-hangzhou"
	}

	client, err := p.getSDKClient(region, ak, sk)
	if err != nil {
		return nil, err
	}

	req := requests.NewCommonRequest()
	req.Method = "POST"
	if d, ok := p.customDomain["DescribeMetricList"]; ok {
		req.Domain = d
	} else {
		req.Domain = "metrics.aliyuncs.com"
	}
	req.Version = "2019-01-01"
	req.ApiName = "DescribeMetricList"
	req.Product = "Cms"
	req.QueryParams["Namespace"] = "acs_ecs_dashboard"
	req.QueryParams["MetricName"] = cmsMetricName
	req.QueryParams["Dimensions"] = fmt.Sprintf(`[{"instanceId":"%s"}]`, params.ResRef)

	period := params.PeriodSec
	if period <= 0 {
		period = 300 // 5 minutes default
	}
	req.QueryParams["Period"] = fmt.Sprintf("%d", period)

	if params.StartTimeMs > 0 {
		req.QueryParams["StartTime"] = fmt.Sprintf("%d", params.StartTimeMs)
	}
	if params.EndTimeMs > 0 {
		req.QueryParams["EndTime"] = fmt.Sprintf("%d", params.EndTimeMs)
	}
	req.QueryParams["Length"] = "1440"

	resp, err := p.doWithRetry(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("aliyun cms DescribeMetricList failed: %w", err)
	}

	var cmsResp struct {
		Code       string `json:"Code"`
		Message    string `json:"Message"`
		Datapoints string `json:"Datapoints"`
	}
	if err := json.Unmarshal(resp.GetHttpContentBytes(), &cmsResp); err != nil {
		return nil, fmt.Errorf("aliyun cms parse response failed: %w", err)
	}
	if cmsResp.Code != "" && cmsResp.Code != "200" {
		return nil, fmt.Errorf("aliyun cms returned error code %s: %s", cmsResp.Code, cmsResp.Message)
	}

	var rawDatapoints []struct {
		Timestamp int64    `json:"timestamp"`
		Average   *float64 `json:"Average"`
		Value     *float64 `json:"Value"`
		Maximum   *float64 `json:"Maximum"`
		Minimum   *float64 `json:"Minimum"`
	}

	if cmsResp.Datapoints != "" {
		if err := json.Unmarshal([]byte(cmsResp.Datapoints), &rawDatapoints); err != nil {
			return nil, fmt.Errorf("aliyun cms parse Datapoints array failed: %w", err)
		}
	}

	var points []provider.MetricPoint
	for _, dp := range rawDatapoints {
		var val float64
		if dp.Average != nil {
			val = *dp.Average
		} else if dp.Value != nil {
			val = *dp.Value
		} else if dp.Maximum != nil {
			val = *dp.Maximum
		} else if dp.Minimum != nil {
			val = *dp.Minimum
		} else {
			continue
		}

		if isRateMetric {
			// CMS rates are Bits/s -> convert to Bytes/s (08-field-map.md)
			val = val / 8.0
		}

		points = append(points, provider.MetricPoint{
			TsMs:  dp.Timestamp,
			Value: val,
		})
	}

	sort.Slice(points, func(i, j int) bool {
		return points[i].TsMs < points[j].TsMs
	})

	if points == nil {
		points = []provider.MetricPoint{}
	}

	return &provider.MetricListResult{
		ResRef:     params.ResRef,
		MetricCode: params.MetricCode,
		Points:     points,
	}, nil
}
