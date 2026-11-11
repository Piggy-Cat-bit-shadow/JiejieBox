//go:build windows

package main

// A three-RPC probe against the real daemon process, over the daemon's documented development
// transport. It proves the product binary answers PreviewServiceChange, GetLastServiceChange and
// GetLastConfigRecovery with real documents; it deliberately does NOT apply a configuration,
// because applying goes through DesktopService, which requires the Windows peer identity that
// only the authenticated pipe provides.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestProductConfigurationRPCs(t *testing.T) {
	binary := os.Getenv("BOXDD_ACCEPTANCE_BIN")
	if binary == "" {
		t.Skip("set BOXDD_ACCEPTANCE_BIN to the daemon binary")
	}
	output := os.Getenv("BOXDD_ACCEPTANCE_OUT")
	if output == "" {
		output = t.TempDir()
	}
	workDir := filepath.Join(output, "rpc-work")
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	logFile, err := os.Create(filepath.Join(output, "rpc-daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "-D", workDir, "--listen", fmt.Sprintf("127.0.0.1:%d", port))
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Kill, reap and only then close the log: t.TempDir's cleanup runs RemoveAll on this directory,
	// and an unreaped child on Windows still holds the log file open, which turns a passing test into
	// a teardown failure.
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = logFile.Close()
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not listen: %v", dialErr)
		}
		time.Sleep(100 * time.Millisecond)
	}

	client, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	service := daemon.NewStartedServiceClient(client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := `{"log":{"level":"warn"},"inbounds":[{"type":"http","tag":"in","listen":"127.0.0.1","listen_port":18080}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	plan, err := service.PreviewServiceChange(ctx, &daemon.PreviewServiceChangeRequest{ConfigContent: config})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	report, err := service.GetLastServiceChange(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("last change: %v", err)
	}
	recovery, err := service.GetLastConfigRecovery(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("last recovery: %v", err)
	}

	records := map[string]any{
		"binary":        binary,
		"preview":       plan.Json,
		"last_change":   report.Json,
		"last_recovery": recovery.Json,
	}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(output, "configuration-rpcs.json")
	if err := os.WriteFile(evidencePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("evidence written to %s", evidencePath)

	var decoded daemon.ServiceChangePlan
	if err := json.Unmarshal([]byte(plan.Json), &decoded); err != nil {
		t.Fatalf("preview document is not a plan: %v", err)
	}
	if decoded.Started {
		t.Fatalf("a fresh daemon previewed as started")
	}
	if report.Json != "null" || recovery.Json != "null" {
		t.Fatalf("a fresh daemon reported a change or a recovery: %s / %s", report.Json, recovery.Json)
	}
}
