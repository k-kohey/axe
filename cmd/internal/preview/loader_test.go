package preview

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSendReloadCommand_OK(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Start a mock loader server
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	var received string
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		received = string(buf[:n])
		_, _ = conn.Write([]byte("OK\n"))
	}()

	err = sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
	if err != nil {
		t.Fatalf("sendReloadCommand returned error: %v", err)
	}

	<-done
	if received != "/tmp/thunk_0.dylib\n" {
		t.Errorf("received = %q, want %q", received, "/tmp/thunk_0.dylib\n")
	}
}

func TestSendReloadCommand_Error(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERR:dlopen failed: symbol not found\n"))
	}()

	err = sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
	if err == nil {
		t.Fatal("expected error for ERR response")
	}
	if got := err.Error(); got != "loader error: dlopen failed: symbol not found" {
		t.Errorf("error = %q", got)
	}
}

func TestSendReloadCommand_NoSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	// Remove socket file to make sure it doesn't exist
	_ = os.Remove(sockPath)

	err := sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
	if err == nil {
		t.Fatal("expected error when socket does not exist")
	}
}
