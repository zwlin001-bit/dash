package aliyun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"

	"dash/internal/provider"
	"dash/internal/provider/aliyun"
)

func createMockSDKClient(serverURL string) (*sdk.Client, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	client, err := sdk.NewClientWithAccessKey("cn-hangzhou", "test-ak", "test-sk")
	if err != nil {
		return nil, err
	}
	client.GetConfig().Scheme = "http"
	client.GetConfig().AutoRetry = false
	client.GetConfig().MaxRetryTime = 0
	client.Domain = u.Host
	return client, nil
}

func TestCDTTrafficSumAndCache(t *testing.T) {
	var cdtCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cdtCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"TrafficDetails": {
				"TrafficDetail": [
					{ "Traffic": 1000000, "ProductType": "ECS" },
					{ "Traffic": 2500000, "ProductType": "SLB" }
				]
			}
		}`))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("ListCdtInternetTraffic", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{
		"access_key_id":     "LTAI5test123",
		"access_key_secret": "secret999",
	}

	// First query
	res1, err := p.GetCDTTraffic(ctx, cred)
	if err != nil {
		t.Fatalf("GetCDTTraffic 1 failed: %v", err)
	}
	if res1.TrafficBytes != 3500000 {
		t.Fatalf("expected sum 3500000 bytes, got %d", res1.TrafficBytes)
	}
	if atomic.LoadInt32(&cdtCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", atomic.LoadInt32(&cdtCalls))
	}

	// Second query within cache TTL: MUST be cached and NOT call remote API again
	res2, err := p.GetCDTTraffic(ctx, cred)
	if err != nil {
		t.Fatalf("GetCDTTraffic 2 failed: %v", err)
	}
	if res2.TrafficBytes != 3500000 {
		t.Fatalf("cached traffic bytes mismatch: got %d", res2.TrafficBytes)
	}
	if atomic.LoadInt32(&cdtCalls) != 1 {
		t.Fatalf("expected CDT traffic to be cached, but API called %d times", atomic.LoadInt32(&cdtCalls))
	}
}

func TestBSSFailureIsolation(t *testing.T) {
	// ECS succeeds, BSS returns 403 NoPermission
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := r.URL.Query().Get("Action")
		if body == "" {
			_ = r.ParseForm()
			body = r.Form.Get("Action")
		}

		w.Header().Set("Content-Type", "application/json")
		if body == "DescribeInstanceBill" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"Code":"NoPermission","Message":"You are not authorized"}`))
			return
		}

		// ECS DescribeInstances response
		_, _ = w.Write([]byte(`{
			"Instances": {
				"Instance": [
					{
						"InstanceId": "i-inst123",
						"InstanceName": "hk-proxy-01",
						"Status": "Running",
						"RegionId": "cn-hongkong",
						"Cpu": 2,
						"Memory": 2048,
						"PublicIpAddress": { "IpAddress": ["8.8.8.8"] },
						"VpcAttributes": { "PrivateIpAddress": { "IpAddress": ["172.16.0.1"] } }
					}
				]
			}
		}`))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("DescribeInstances", u.Host),
		aliyun.WithCustomDomain("DescribeInstanceBill", u.Host),
		aliyun.WithRetryDelays([]time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}),
	)

	ctx := context.Background()
	cred := map[string]string{
		"access_key_id":     "LTAI5test123",
		"access_key_secret": "secret999",
	}

	resources, err := p.ListResources(ctx, cred, "cn-hongkong", "instance", "china")
	if err != nil {
		t.Fatalf("ListResources should succeed even when BSS fails, but got: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	r := resources[0]
	if r.Ref != "i-inst123" {
		t.Fatalf("expected ref i-inst123, got %s", r.Ref)
	}
	if r.BillError == "" {
		t.Fatal("expected BillError to be populated with BSS error message")
	}
	if r.Status != "running" {
		t.Fatalf("expected normalized status 'running', got %s", r.Status)
	}
}

func TestRetryBehaviorAndNoSecretInError(t *testing.T) {
	var attempts int32
	secretToHide := "SuperSecretKey999XYZ"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// Return 500 server error to trigger retry
		http.Error(w, "internal gateway timeout with secret="+secretToHide, http.StatusBadGateway)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("DescribeRegions", u.Host),
		aliyun.WithRetryDelays([]time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 15 * time.Millisecond}),
	)

	ctx := context.Background()
	cred := map[string]string{
		"access_key_id":     "LTAI5testAK",
		"access_key_secret": secretToHide,
	}

	res, err := p.Healthcheck(ctx, cred, "cn-hangzhou")
	if err != nil {
		t.Fatalf("healthcheck returned error instead of result: %v", err)
	}
	if res.OK {
		t.Fatal("expected healthcheck OK=false")
	}

	// Verify retry count: initial try + 3 retries = 4 total attempts
	if atomic.LoadInt32(&attempts) != 4 {
		t.Fatalf("expected 4 attempts (1 initial + 3 retries), got %d", atomic.LoadInt32(&attempts))
	}

	// Verify no secret leak in error message
	if strings.Contains(res.Message, secretToHide) {
		t.Fatalf("secret leaked in error message: %s", res.Message)
	}
}

func TestInstanceStartStopAction(t *testing.T) {
	var requestedAction string
	var requestedInstance string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		requestedAction = r.Form.Get("Action")
		requestedInstance = r.Form.Get("InstanceId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RequestId":"req-123"}`))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("StartInstance", u.Host),
		aliyun.WithCustomDomain("StopInstance", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{"access_key_id": "ak", "access_key_secret": "sk"}

	// 1. Start action
	resp, err := p.Action(ctx, provider.ActionParams{
		Credential: cred,
		Region:     "cn-hongkong",
		Kind:       "instance",
		Ref:        "i-target999",
		Action:     "start",
	})
	if err != nil {
		t.Fatalf("start action failed: %v", err)
	}
	if resp.Status != "succeeded" {
		t.Fatalf("expected succeeded, got %s", resp.Status)
	}
	if requestedAction != "StartInstance" || requestedInstance != "i-target999" {
		t.Fatalf("unexpected request: action=%s, instance=%s", requestedAction, requestedInstance)
	}

	// 2. Stop action
	resp, err = p.Action(ctx, provider.ActionParams{
		Credential: cred,
		Region:     "cn-hongkong",
		Kind:       "instance",
		Ref:        "i-target999",
		Action:     "stop",
	})
	if err != nil {
		t.Fatalf("stop action failed: %v", err)
	}
	if resp.Status != "succeeded" {
		t.Fatalf("expected succeeded, got %s", resp.Status)
	}
	if requestedAction != "StopInstance" || requestedInstance != "i-target999" {
		t.Fatalf("unexpected request: action=%s, instance=%s", requestedAction, requestedInstance)
	}
}

func TestGroupingCallsDescribeAndCDT(t *testing.T) {
	var describeCalls int32
	var cdtCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		if action == "" {
			action = r.URL.Query().Get("Action")
		}

		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "DescribeInstances":
			atomic.AddInt32(&describeCalls, 1)
			reg := r.Form.Get("RegionId")
			// Return multiple instances per region
			var insts []map[string]any
			for i := 0; i < 7; i++ {
				insts = append(insts, map[string]any{
					"InstanceId":   fmt.Sprintf("i-%s-%d", reg, i),
					"InstanceName": fmt.Sprintf("name-%s-%d", reg, i),
					"Status":       "Running",
					"RegionId":     reg,
				})
			}
			data, _ := json.Marshal(map[string]any{
				"Instances": map[string]any{
					"Instance": insts,
				},
			})
			_, _ = w.Write(data)

		case "ListCdtInternetTraffic":
			atomic.AddInt32(&cdtCalls, 1)
			_, _ = w.Write([]byte(`{
				"TrafficDetails": {
					"TrafficDetail": [
						{ "Traffic": 88888888, "ProductType": "ECS" }
					]
				}
			}`))

		case "DescribeInstanceBill":
			_, _ = w.Write([]byte(`{"Data":{"Items":{"Item":[]}}}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("DescribeInstances", u.Host),
		aliyun.WithCustomDomain("ListCdtInternetTraffic", u.Host),
		aliyun.WithCustomDomain("DescribeInstanceBill", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{"access_key_id": "ak", "access_key_secret": "sk"}
	regions := []string{"cn-hangzhou", "cn-shanghai", "cn-hongkong"}

	// 1. Discover 3 regions
	resources, err := p.Discover(ctx, cred, regions, "china")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(resources) != 21 {
		t.Fatalf("expected 21 instances across 3 regions, got %d", len(resources))
	}

	// 2. Query CDT traffic
	cdt, err := p.GetCDTTraffic(ctx, cred)
	if err != nil {
		t.Fatalf("GetCDTTraffic failed: %v", err)
	}
	if cdt.TrafficBytes != 88888888 {
		t.Fatalf("unexpected traffic: %d", cdt.TrafficBytes)
	}

	// Criterion 10: 20+ instances across 3 regions -> DescribeInstances calls = 3, CDT calls = 1
	if atomic.LoadInt32(&describeCalls) != 3 {
		t.Fatalf("expected DescribeInstances calls = 3, got %d", atomic.LoadInt32(&describeCalls))
	}
	if atomic.LoadInt32(&cdtCalls) != 1 {
		t.Fatalf("expected CDT calls = 1, got %d", atomic.LoadInt32(&cdtCalls))
	}
}

func TestListMetrics(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("Action")
		if action == "" {
			_ = r.ParseForm()
			action = r.Form.Get("Action")
		}

		w.Header().Set("Content-Type", "application/json")

		switch action {
		case "ListCdtInternetTraffic":
			_, _ = w.Write([]byte(`{
				"TrafficDetails": {
					"TrafficDetail": [
						{ "Traffic": 104857600, "ProductType": "ECS" }
					]
				}
			}`))
		case "DescribeMetricList":
			metricName := r.URL.Query().Get("MetricName")
			if metricName == "" {
				metricName = r.Form.Get("MetricName")
			}
			if metricName == "CPUUtilization" {
				_, _ = w.Write([]byte(`{
					"Code": "200",
					"Datapoints": "[{\"timestamp\":1700000000000,\"Average\":15.5},{\"timestamp\":1700000300000,\"Average\":20.0}]"
				}`))
			} else if metricName == "InternetOutRate" {
				// 8000000 bits/s = 1000000 bytes/s
				_, _ = w.Write([]byte(`{
					"Code": "200",
					"Datapoints": "[{\"timestamp\":1700000000000,\"Average\":8000000.0}]"
				}`))
			} else {
				_, _ = w.Write([]byte(`{"Code":"200","Datapoints":"[]"}`))
			}
		default:
			http.Error(w, "unknown action", 400)
		}
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("ListCdtInternetTraffic", u.Host),
		aliyun.WithCustomDomain("DescribeMetricList", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{"access_key_id": "ak", "access_key_secret": "sk"}

	// 1. Account CDT traffic
	resCDT, err := p.ListMetrics(ctx, provider.MetricListParams{
		Credential: cred,
		ResRef:     "_account",
		MetricCode: "traffic_month_up",
	})
	if err != nil {
		t.Fatalf("ListMetrics for _account failed: %v", err)
	}
	if len(resCDT.Points) != 1 || resCDT.Points[0].Value != 104857600 {
		t.Fatalf("unexpected CDT points: %+v", resCDT.Points)
	}

	// 2. ECS CPU
	resCPU, err := p.ListMetrics(ctx, provider.MetricListParams{
		Credential: cred,
		Region:     "cn-hangzhou",
		ResRef:     "i-test123",
		MetricCode: "cpu_pct",
	})
	if err != nil {
		t.Fatalf("ListMetrics for cpu_pct failed: %v", err)
	}
	if len(resCPU.Points) != 2 || resCPU.Points[0].Value != 15.5 || resCPU.Points[1].Value != 20.0 {
		t.Fatalf("unexpected CPU points: %+v", resCPU.Points)
	}

	// 3. ECS Net Up Rate (bits/s -> bytes/s)
	resNet, err := p.ListMetrics(ctx, provider.MetricListParams{
		Credential: cred,
		Region:     "cn-hangzhou",
		ResRef:     "i-test123",
		MetricCode: "net_up_bps",
	})
	if err != nil {
		t.Fatalf("ListMetrics for net_up_bps failed: %v", err)
	}
	if len(resNet.Points) != 1 || resNet.Points[0].Value != 1000000.0 {
		t.Fatalf("expected 1000000 bytes/s, got %v", resNet.Points[0].Value)
	}
}

func TestListBills(t *testing.T) {
	var overviewCalls, billCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		action := r.Form.Get("Action")

		switch action {
		case "QueryBillOverview":
			atomic.AddInt32(&overviewCalls, 1)
			_, _ = w.Write([]byte(`{
				"Data": {
					"BillingCycle": "2026-09",
					"AccountCurrency": "CNY",
					"Items": {
						"Item": [
							{
								"PretaxGrossAmount": 1250.00,
								"InvoiceDiscount": 15.44,
								"PretaxAmount": 1234.56,
								"PaymentAmount": 1234.56,
								"Currency": "CNY"
							}
						]
					}
				}
			}`))
		case "QueryInstanceBill":
			atomic.AddInt32(&billCalls, 1)
			pageNum := r.Form.Get("PageNum")
			if pageNum == "1" {
				_, _ = w.Write([]byte(`{
					"Data": {
						"TotalCount": 3,
						"PageNum": 1,
						"PageSize": 2,
						"Items": {
							"Item": [
								{
									"InstanceID": "i-test1",
									"NickName": "web-ecs-01",
									"ProductCode": "ecs",
									"PaymentAmount": 85.00,
									"Usage": "720",
									"UsageUnit": "小时"
								},
								{
									"InstanceID": "d-disk1",
									"NickName": "data-disk-01",
									"ProductCode": "disk",
									"PaymentAmount": 30.00,
									"Usage": "40",
									"UsageUnit": "GB"
								}
							]
						}
					}
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"Data": {
						"TotalCount": 3,
						"PageNum": 2,
						"PageSize": 2,
						"Items": {
							"Item": [
								{
									"InstanceID": "",
									"NickName": "共享公网带宽包",
									"ProductCode": "cbwp",
									"PaymentAmount": 50.00,
									"Usage": "100",
									"UsageUnit": "GB"
								}
							]
						}
					}
				}`))
			}
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("QueryBillOverview", u.Host),
		aliyun.WithCustomDomain("QueryInstanceBill", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{"access_key_id": "test_ak", "access_key_secret": "test_sk"}

	res, err := p.ListBills(ctx, cred, "2026-09", "china")
	if err != nil {
		t.Fatalf("ListBills failed: %v", err)
	}

	if res.Period != "2026-09" {
		t.Errorf("expected period 2026-09, got %s", res.Period)
	}
	if res.Currency != "CNY" {
		t.Errorf("expected currency CNY, got %s", res.Currency)
	}
	if res.TotalAmount != 1234.56 {
		t.Errorf("expected total amount 1234.56, got %f", res.TotalAmount)
	}
	if res.DiscountAmount != 15.44 {
		t.Errorf("expected discount 15.44, got %f", res.DiscountAmount)
	}
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items across 2 pages, got %d", len(res.Items))
	}

	// Verify item 1: instance
	if res.Items[0].ResKind != "instance" || res.Items[0].ResRef != "i-test1" || res.Items[0].Amount != 85.00 || res.Items[0].UsageText != "720 小时" {
		t.Errorf("unexpected item 0: %+v", res.Items[0])
	}
	// Verify item 2: disk
	if res.Items[1].ResKind != "disk" || res.Items[1].ResRef != "d-disk1" || res.Items[1].Amount != 30.00 {
		t.Errorf("unexpected item 1: %+v", res.Items[1])
	}
	// Verify item 3: unassociated bandwidth package
	if res.Items[2].ResKind != "bandwidth" || res.Items[2].ResRef != "" || res.Items[2].Amount != 50.00 {
		t.Errorf("unexpected item 2: %+v", res.Items[2])
	}

	if atomic.LoadInt32(&overviewCalls) != 1 {
		t.Errorf("expected 1 overview call, got %d", atomic.LoadInt32(&overviewCalls))
	}
	if atomic.LoadInt32(&billCalls) != 2 {
		t.Errorf("expected 2 instance bill calls for pagination, got %d", atomic.LoadInt32(&billCalls))
	}
}

func TestDescribeRegionsAndFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("Action")
		if action == "" {
			_ = r.ParseForm()
			action = r.Form.Get("Action")
		}

		if action == "DescribeRegions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"Regions": {
					"Region": [
						{ "RegionId": "cn-hangzhou", "LocalName": "华东1（杭州）" },
						{ "RegionId": "eu-central-1", "LocalName": "欧洲中部 1 (法兰克福)" }
					]
				}
			}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	p := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return createMockSDKClient(ts.URL)
		}),
		aliyun.WithCustomDomain("DescribeRegions", u.Host),
	)

	ctx := context.Background()
	cred := map[string]string{"access_key_id": "ak", "access_key_secret": "sk"}

	// 1. Success case: returns from API
	regs, err := p.ListRegions(ctx, cred)
	if err != nil {
		t.Fatalf("ListRegions failed: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regs))
	}
	if regs[0].RegionID != "cn-hangzhou" || regs[1].RegionID != "eu-central-1" {
		t.Errorf("unexpected regions: %+v", regs)
	}

	// 2. Fallback case: empty cred or server error falls back to DefaultBuiltinRegions
	pErr := aliyun.NewProvider(
		aliyun.WithClientHook(func(region, ak, sk string) (*sdk.Client, error) {
			return nil, fmt.Errorf("network connection error")
		}),
	)
	fallbackRegs, err := pErr.ListRegions(ctx, cred)
	if err != nil {
		t.Fatalf("fallback expected nil error, got: %v", err)
	}
	if len(fallbackRegs) == 0 {
		t.Fatalf("expected builtin fallback regions, got empty")
	}
	// Check that default builtin includes standard regions
	foundHangzhou := false
	for _, r := range fallbackRegs {
		if r.RegionID == "cn-hangzhou" && r.LocalName != "" {
			foundHangzhou = true
			break
		}
	}
	if !foundHangzhou {
		t.Errorf("expected cn-hangzhou in builtin fallback list")
	}
}
