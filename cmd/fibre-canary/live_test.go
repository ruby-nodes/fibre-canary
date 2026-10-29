package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestLiveMochaSmoke(t *testing.T) {
	if os.Getenv("FIBRE_CANARY_LIVE_MOCHA") != "1" {
		t.Skip("set FIBRE_CANARY_LIVE_MOCHA=1 to authorize a paid Mocha run")
	}
	configPath := os.Getenv("FIBRE_CANARY_LIVE_CONFIG")
	if configPath == "" {
		t.Fatal("FIBRE_CANARY_LIVE_CONFIG must name an absolute config path")
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", "--config", configPath}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("live run exit code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status=succeeded") {
		t.Fatalf("live run did not report successful verification:\n%s", stdout.String())
	}
}
