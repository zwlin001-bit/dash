package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dash/internal/logx"
	"dash/internal/provider"
	"dash/internal/provider/aliyun"
)

var (
	version   = "dev"
	gitCommit = "none"
	buildTime = "unknown"
)

func main() {
	var (
		socketPath  string
		showVersion bool
	)
	flag.StringVar(&socketPath, "socket", "", "Path to unix domain socket")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("dash-provider-aliyun %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		return
	}

	if socketPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: dash-provider-aliyun -socket <socket_path>\n")
		os.Exit(1)
	}

	// Ensure socket directory exists
	if dir := filepath.Dir(socketPath); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	// Remove stale socket if exists
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on socket %s: %v\n", socketPath, err)
		os.Exit(1)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	p := aliyun.NewProvider()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = listener.Close()
		_ = os.Remove(socketPath)
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handleConn(conn, p)
	}
}

func handleConn(conn net.Conn, p *aliyun.Provider) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req provider.Request
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(conn, "", -32700, "Parse error: "+err.Error())
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		handleRequest(ctx, conn, req, p)
		cancel()
	}
}

func handleRequest(ctx context.Context, conn net.Conn, req provider.Request, p *aliyun.Provider) {
	switch req.Method {
	case "provider.describe":
		sendResult(conn, req.ID, p.Describe())

	case "provider.healthcheck":
		var params provider.HealthcheckParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.Healthcheck(ctx, params.Credential, params.Region)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	case "resource.list":
		var params provider.ListResourcesParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.ListResources(ctx, params.Credential, params.Region, params.Kind, params.AccountSite)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	case "provider.discover":
		var params provider.DiscoverParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.Discover(ctx, params.Credential, params.Regions, params.AccountSite)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	case "traffic.get":
		var params provider.TrafficParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.GetCDTTraffic(ctx, params.Credential)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	case "resource.action":
		var params provider.ActionParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.Action(ctx, params)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	case "job.poll":
		var params provider.PollJobParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res := provider.PollJobResponse{
			JobHandle: params.JobHandle,
			Status:    "succeeded",
			Message:   "Action completed",
		}
		sendResult(conn, req.ID, res)

	case "metric.list":
		var params provider.MetricListParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
		res, err := p.ListMetrics(ctx, params)
		if err != nil {
			sendError(conn, req.ID, -32000, err.Error())
			return
		}
		sendResult(conn, req.ID, res)

	default:
		sendError(conn, req.ID, -32601, fmt.Sprintf("Method %s not found", req.Method))
	}
}

func sendResult(conn net.Conn, id string, result any) {
	resBytes, err := json.Marshal(result)
	if err != nil {
		sendError(conn, id, -32603, "Internal error marshaling result: "+err.Error())
		return
	}
	resp := provider.Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resBytes,
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func sendError(conn net.Conn, id string, code int, msg string) {
	resp := provider.Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &provider.RPCError{
			Code:    code,
			Message: logx.Redact(msg),
		},
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
