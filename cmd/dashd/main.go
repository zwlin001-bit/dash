package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

var (
	version   = "dev"
	gitCommit = "none"
	buildTime = "unknown"
)

// App 包含 dashd 运行时的核心组件（配置、数据库、路由、后台任务等）。
// 供各模块在 Register 时进行依赖注入和路由装配。
type App struct {
	// 后续任务按需扩充字段
}

// Module 定义服务端子模块的注册契约。
type Module interface {
	Name() string
	Register(*App) error // 注册路由、后台任务、依赖注入
}

// modules 为所有需要装配进 dashd 的模块列表。
// 约束：后续任务只允许向此列表追加模块，不改动装配与启动逻辑。
var modules = []Module{
	// 后续任务每人加一行
}

// RegisterModules 按序执行各模块的注册逻辑。
func RegisterModules(app *App, mods []Module) error {
	for _, m := range mods {
		if err := m.Register(app); err != nil {
			return fmt.Errorf("module %s: %w", m.Name(), err)
		}
	}
	return nil
}

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Parse()

	if showVersion || (len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "-v")) {
		fmt.Printf("dashd %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		os.Exit(0)
	}

	app := &App{}
	if err := RegisterModules(app, modules); err != nil {
		log.Fatalf("failed to register modules: %v", err)
	}

	log.Printf("dashd %s initialized with %d modules", version, len(modules))
}
