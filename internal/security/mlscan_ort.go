//go:build ORT

package security

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"
)

var (
	mlScanner     *ONNXGuardScanner
	mlScannerOnce sync.Once
	mlScannerErr  error
)

func initMLScanner() {
	modelPath := os.Getenv("MODEL_PATH")
	if modelPath == "" {
		modelPath = "/opt/huggingface/hub/models--protectai--deberta-v3-base-prompt-injection-v2/snapshots/e6535ca4ce3ba852083e75ec585d7c8aeb4be4c5/onnx"
	}
	ortLibPath := os.Getenv("ORT_LIB_PATH")
	if ortLibPath == "" {
		ortLibPath = "/usr/lib"
	}

	ctx := context.Background()
	session, err := hugot.NewORTSession(ctx,
		options.WithOnnxLibraryPath(ortLibPath),
		options.WithIntraOpNumThreads(4),
	)
	if err != nil {
		mlScannerErr = fmt.Errorf("creating ORT session: %w", err)
		return
	}

	config := hugot.TextClassificationConfig{
		ModelPath: modelPath,
		Name:      "injection-scanner",
		Options: []hugot.TextClassificationOption{
			pipelines.WithSoftmax(),
			pipelines.WithSingleLabel(),
		},
	}
	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		session.Destroy()
		mlScannerErr = fmt.Errorf("creating pipeline: %w", err)
		return
	}

	scanner, err := NewONNXGuardScanner(pipeline, modelPath, 0, "")
	if err != nil {
		session.Destroy()
		mlScannerErr = fmt.Errorf("creating scanner: %w", err)
		return
	}

	mlScanner = scanner
}

// MLScanAvailable reports whether the native ONNX ML scanner is compiled in.
func MLScanAvailable() bool { return true }

// RunMLScan runs the ONNX-based prompt injection scanner. Initializes the
// model session on first call (lazy singleton). Returns safe=true if the
// scanner fails to initialize (fail-open for Path A).
func RunMLScan(text string) ScanResult {
	mlScannerOnce.Do(initMLScanner)

	if mlScannerErr != nil {
		fmt.Fprintf(os.Stderr, "WARN: ML scanner unavailable: %v\n", mlScannerErr)
		return ScanResult{Safe: true}
	}

	return mlScanner.Scan(text)
}
