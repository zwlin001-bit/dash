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

func formatVersion() string {
	return fmt.Sprintf("dash-agent %s (commit: %s, built: %s)", version, gitCommit, buildTime)
}

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Parse()

	if showVersion || (len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "-v")) {
		fmt.Println(formatVersion())
		os.Exit(0)
	}

	log.Printf("dash-agent %s starting", version)
}
