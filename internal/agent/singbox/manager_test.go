package singbox

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagerRestartKeepsNewProcessState(t *testing.T) {
	if testing.Short() {
		t.Skip("process lifecycle test")
	}

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "fake-sing-box")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.binaryPath = binaryPath
	m.configPath = configPath
	m.logPath = filepath.Join(dir, "sing-box.log")
	m.pidPath = filepath.Join(dir, "sing-box.pid")
	m.installer = NewInstaller(binaryPath)
	m.restartDelay = 10 * time.Millisecond

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })
	first := m.Status()
	if !first.Running || first.PID <= 0 {
		t.Fatalf("unexpected initial status: %+v", first)
	}

	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() error: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // allow the old watcher to finish

	after := m.Status()
	if !after.Running || after.PID <= 0 || after.PID == first.PID {
		t.Fatalf("new process state was lost after restart: first=%+v after=%+v", first, after)
	}
	pidData, err := os.ReadFile(m.pidPath)
	if err != nil {
		t.Fatalf("new PID file missing: %v", err)
	}
	if strings.TrimSpace(string(pidData)) != strconv.Itoa(after.PID) {
		t.Fatalf("PID file contains %q, want %d", pidData, after.PID)
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if status := m.Status(); status.Running || status.PID != 0 {
		t.Fatalf("unexpected stopped status: %+v", status)
	}
}

func TestWriteSecretFileIsAtomicAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secret.json")
	for _, content := range []string{"first", "second"} {
		if err := writeSecretFile(path, []byte(content)); err != nil {
			t.Fatalf("writeSecretFile() error: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content {
			t.Fatalf("got %q, want %q", data, content)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("file mode = %o, want 600", got)
		}
	}
}
