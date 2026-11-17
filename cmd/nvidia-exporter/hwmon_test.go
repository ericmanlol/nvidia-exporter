package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenHwmonRetainsReaderAfterInitialFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if h := openHwmon("", logger); h != nil {
		t.Fatal("empty selection should leave hwmon disabled")
	}
	dir := filepath.Join(t.TempDir(), "hwmon")
	h := openHwmon(dir, logger)
	if h == nil {
		t.Fatal("configured reader discarded after initial read failure")
	}
	if _, err := h.Read(); err == nil {
		t.Fatal("missing sensors should report an error")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("45000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := h.Read()
	if err != nil || st.Temps["temp1"] != 45 {
		t.Fatalf("reader did not recover: %+v, %v", st, err)
	}
}
