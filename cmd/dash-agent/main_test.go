package main

import (
	"strings"
	"testing"
)

func TestFormatVersion(t *testing.T) {
	v := formatVersion()
	if !strings.HasPrefix(v, "dash-agent ") {
		t.Fatalf("unexpected version format: %s", v)
	}
}
