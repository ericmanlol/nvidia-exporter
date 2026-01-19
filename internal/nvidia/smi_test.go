package nvidia

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCSV_SingleGPU(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "single-gpu.csv"))
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	stats, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}

	if len(stats) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(stats))
	}

	gpu := stats[0]
	if gpu.Index != 0 {
		t.Errorf("expected index 0, got %d", gpu.Index)
	}
	if gpu.Name != "NVIDIA GeForce RTX 3080" {
		t.Errorf("expected name 'NVIDIA GeForce RTX 3080', got '%s'", gpu.Name)
	}
	if gpu.UUID != "GPU-12345678-1234-1234-1234-123456789abc" {
		t.Errorf("expected UUID 'GPU-12345678-1234-1234-1234-123456789abc', got '%s'", gpu.UUID)
	}
	if gpu.UtilizationGPU != 45 {
		t.Errorf("expected UtilizationGPU 45, got %f", gpu.UtilizationGPU)
	}
	if gpu.UtilizationMem != 32 {
		t.Errorf("expected UtilizationMem 32, got %f", gpu.UtilizationMem)
	}
	// 4096 MiB = 4294967296 bytes
	if gpu.MemoryUsed != 4294967296 {
		t.Errorf("expected MemoryUsed 4294967296, got %d", gpu.MemoryUsed)
	}
	// 10240 MiB = 10737418240 bytes
	if gpu.MemoryTotal != 10737418240 {
		t.Errorf("expected MemoryTotal 10737418240, got %d", gpu.MemoryTotal)
	}
	if gpu.Temperature != 65 {
		t.Errorf("expected Temperature 65, got %f", gpu.Temperature)
	}
	if gpu.PowerDraw != 220.50 {
		t.Errorf("expected PowerDraw 220.50, got %f", gpu.PowerDraw)
	}
	if gpu.PowerLimit != 320.00 {
		t.Errorf("expected PowerLimit 320.00, got %f", gpu.PowerLimit)
	}
	if gpu.FanSpeed != 75 {
		t.Errorf("expected FanSpeed 75, got %f", gpu.FanSpeed)
	}
	if gpu.ClockGraphics != 1905 {
		t.Errorf("expected ClockGraphics 1905, got %f", gpu.ClockGraphics)
	}
	if gpu.ClockMemory != 9501 {
		t.Errorf("expected ClockMemory 9501, got %f", gpu.ClockMemory)
	}
	if gpu.PerformanceState != 0 {
		t.Errorf("expected PerformanceState 0, got %d", gpu.PerformanceState)
	}
}

func TestParseCSV_MultiGPU(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "multi-gpu.csv"))
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	stats, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}

	if len(stats) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(stats))
	}

	// Check first GPU
	if stats[0].Index != 0 {
		t.Errorf("expected first GPU index 0, got %d", stats[0].Index)
	}
	if stats[0].Name != "NVIDIA GeForce RTX 3080" {
		t.Errorf("expected first GPU name 'NVIDIA GeForce RTX 3080', got '%s'", stats[0].Name)
	}

	// Check second GPU
	if stats[1].Index != 1 {
		t.Errorf("expected second GPU index 1, got %d", stats[1].Index)
	}
	if stats[1].Name != "NVIDIA GeForce RTX 3090" {
		t.Errorf("expected second GPU name 'NVIDIA GeForce RTX 3090', got '%s'", stats[1].Name)
	}
	if stats[1].PerformanceState != 2 {
		t.Errorf("expected second GPU PerformanceState 2, got %d", stats[1].PerformanceState)
	}
}

func TestParseCSV_WithNA(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "with-na.csv"))
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	stats, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}

	if len(stats) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(stats))
	}

	gpu := stats[0]

	// N/A values should be 0
	if gpu.UtilizationMem != 0 {
		t.Errorf("expected UtilizationMem 0 for N/A, got %f", gpu.UtilizationMem)
	}
	if gpu.PowerDraw != 0 {
		t.Errorf("expected PowerDraw 0 for Not Supported, got %f", gpu.PowerDraw)
	}
	if gpu.FanSpeed != 0 {
		t.Errorf("expected FanSpeed 0 for N/A, got %f", gpu.FanSpeed)
	}

	// Non-N/A values should still be parsed correctly
	if gpu.UtilizationGPU != 45 {
		t.Errorf("expected UtilizationGPU 45, got %f", gpu.UtilizationGPU)
	}
}

func TestParseCSV_MalformedInput(t *testing.T) {
	// Wrong number of fields
	data := []byte("0, NVIDIA GeForce RTX 3080, GPU-123")
	_, err := ParseCSV(data)
	if err == nil {
		t.Error("expected error for malformed input, got nil")
	}
}

func TestParseCSV_EmptyInput(t *testing.T) {
	data := []byte("")
	stats, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty slice, got %d items", len(stats))
	}

	// Also test with just whitespace
	data = []byte("   \n   \n")
	stats, err = ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty slice, got %d items", len(stats))
	}
}
