package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sarv-projects/litepsm/internal/config"
)

func TestWizard_InteractiveAgentSelectionAndCancellation(t *testing.T) {
	tempDir := t.TempDir()
	paths := &config.PlatformPaths{
		ConfigRoot: tempDir + "/config",
		DataRoot:   tempDir + "/data",
	}

	// Test quit choice "q"
	var inBuf bytes.Buffer
	var outBuf bytes.Buffer
	inBuf.WriteString("q\n")

	w := NewWizard(&inBuf, &outBuf, paths)
	err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected wizard error: %v", err)
	}

	outStr := outBuf.String()
	if !strings.Contains(outStr, "LitePSM Agent Setup") {
		t.Errorf("expected banner in output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "Setup cancelled") {
		t.Errorf("expected cancellation message, got: %s", outStr)
	}
}

func TestWizard_PrintUsage(t *testing.T) {
	printUsage()
}
