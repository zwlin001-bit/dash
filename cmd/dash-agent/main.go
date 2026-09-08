package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	_ "dash/agent/collect/linux"
	agentrt "dash/agent/runtime"
)

var (
	version   = "0.1.0"
	gitCommit = "none"
	buildTime = "unknown"
)

func formatVersion() string {
	return fmt.Sprintf("dash-agent %s (commit: %s, built: %s)", version, gitCommit, buildTime)
}

func parseCommaSlice(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var res []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func main() {
	// ★ 硬性约束：单核调度、积极 GC、24MB 内存上限保护
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(24 << 20)

	// 日志默认一律写 stderr，不开日志文件、不做轮转
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[dash-agent] ")

	fs := flag.NewFlagSet("dash-agent", flag.ExitOnError)

	var (
		showVersion        bool
		configFile         string
		endpoint           string
		token              string
		stateFile          string
		intervalFast       int
		intervalSlow       int
		factsMaxInterval   int
		collectConns       bool
		includeMountsStr   string
		excludeMountsStr   string
		includeNICsStr     string
		excludeNICsStr     string
		memIncludeCache    bool
		execMode           string
		enableTerminal     bool
		insecureSkipVerify bool
		preferIPVersion    string
		transportMode      string
		nodeID             string
	)

	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit")
	fs.StringVar(&configFile, "config", "/etc/dash-agent/config.json", "path to config file")
	fs.StringVar(&configFile, "c", "/etc/dash-agent/config.json", "path to config file (shorthand)")
	fs.StringVar(&endpoint, "endpoint", "", "server endpoint URL (e.g. wss://example.com)")
	fs.StringVar(&token, "token", "", "agent authentication token")
	fs.StringVar(&nodeID, "node", "", "node identifier (sent as X-Node header)")
	fs.StringVar(&nodeID, "node-id", "", "node identifier (sent as X-Node header)")
	fs.StringVar(&transportMode, "transport", "auto", "transport protocol (auto, http)")
	fs.StringVar(&stateFile, "state-file", "/var/lib/dash-agent/state.json", "path to state file")
	fs.IntVar(&intervalFast, "interval-fast", 5, "fast tier interval in seconds")
	fs.IntVar(&intervalFast, "interval-fast-s", 5, "fast tier interval in seconds")
	fs.IntVar(&intervalSlow, "interval-slow", 60, "slow tier interval in seconds")
	fs.IntVar(&intervalSlow, "interval-slow-s", 60, "slow tier interval in seconds")
	fs.IntVar(&factsMaxInterval, "facts-max-interval", 1800, "facts fallback interval in seconds")
	fs.IntVar(&factsMaxInterval, "facts-max-interval-s", 1800, "facts fallback interval in seconds")
	fs.BoolVar(&collectConns, "collect-conns", true, "enable/disable connection stats collection")
	fs.StringVar(&includeMountsStr, "include-mounts", "", "comma-separated mount points to include")
	fs.StringVar(&excludeMountsStr, "exclude-mounts", "", "comma-separated mount points to exclude")
	fs.StringVar(&includeNICsStr, "include-nics", "", "comma-separated NIC names to include")
	fs.StringVar(&excludeNICsStr, "exclude-nics", "", "comma-separated NIC names to exclude")
	fs.BoolVar(&memIncludeCache, "mem-include-cache", false, "calculate memory as MemTotal - MemFree instead of Available")
	fs.StringVar(&execMode, "exec-mode", "off", "execution mode (off, actions, shell)")
	fs.BoolVar(&enableTerminal, "enable-terminal", false, "enable terminal capability")
	fs.BoolVar(&insecureSkipVerify, "insecure-skip-verify", false, "skip TLS verification (debug only)")
	fs.StringVar(&preferIPVersion, "prefer-ip-version", "", "prefer IP version (4 or 6)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags error: %v\n", err)
		os.Exit(1)
	}

	if showVersion {
		fmt.Println(formatVersion())
		os.Exit(0)
	}

	// 记录命令行显式设置的参数
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})

	flagOverrides := &agentrt.Config{
		ConfigFile:         configFile,
		Endpoint:           endpoint,
		Token:              token,
		NodeID:             nodeID,
		Transport:          transportMode,
		StateFile:          stateFile,
		IntervalFastS:      intervalFast,
		IntervalSlowS:      intervalSlow,
		FactsMaxIntervalS:  factsMaxInterval,
		CollectConns:       collectConns,
		IncludeMounts:      parseCommaSlice(includeMountsStr),
		ExcludeMounts:      parseCommaSlice(excludeMountsStr),
		IncludeNICs:        parseCommaSlice(includeNICsStr),
		ExcludeNICs:        parseCommaSlice(excludeNICsStr),
		MemIncludeCache:    memIncludeCache,
		ExecMode:           execMode,
		EnableTerminal:     enableTerminal,
		InsecureSkipVerify: insecureSkipVerify,
		PreferIPVersion:    preferIPVersion,
	}

	cfg, err := agentrt.LoadConfig(flagOverrides, setFlags)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	rt, err := agentrt.New(cfg, version)
	if err != nil {
		log.Fatalf("failed to initialize runtime: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := rt.Run(ctx); err != nil {
		log.Fatalf("runtime exited with error: %v", err)
	}
}
