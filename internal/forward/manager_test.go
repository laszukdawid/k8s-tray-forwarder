package forward

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/kube"
)

// stubKubectl writes an executable script that mimics a healthy, long-lived
// `kubectl port-forward`: it prints the "Forwarding from" line the supervisor
// waits for to mark a forward running, then execs a long sleep so the tracked
// process blocks until it is killed (exec keeps the same PID, so cancelling the
// command's context actually reaps this process — as it would the real kubectl).
func stubKubectl(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	script := "#!/bin/sh\n" +
		"echo 'Forwarding from 127.0.0.1:15432 -> 5432'\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub kubectl: %v", err)
	}
	return path
}

func testForward() config.Forward {
	return config.Forward{
		ID:         "test-1",
		Type:       config.TypeKubernetes,
		Name:       "Test",
		Context:    "ctx",
		Namespace:  "ns",
		TargetKind: config.KindService,
		TargetName: "svc",
		RemotePort: 5432,
		LocalPort:  15432,
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// TestStopAllAndWaitClosesForwards is the regression guard for clean shutdown:
// once a forward is running, StopAllAndWait must return (which only happens
// after the kubectl process is killed and cmd.Wait reaps it) and leave nothing
// active. If teardown ever stops cancelling the process context, cmd.Wait would
// block forever and this test would hit its watchdog timeout.
func TestStopAllAndWaitClosesForwards(t *testing.T) {
	restore := kube.SetBinaryForTest(stubKubectl(t))
	defer restore()

	m := New(nil, nil)
	f := testForward()
	if err := m.Start(f); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitFor(t, func() bool { return m.Status(f.ID).State == StateRunning },
		5*time.Second, "forward to reach running")

	done := make(chan struct{})
	go func() {
		m.StopAllAndWait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAllAndWait did not return — kubectl process was not torn down")
	}

	if m.Active(f.ID) {
		t.Fatal("forward still active after StopAllAndWait")
	}
}

// TestStartAfterStopIsNoOp guards the terminal-manager hardening: once StopAll
// has run (as on quit), Start must not spin up a new forward. This is what keeps
// a late wg.Add from racing the wg.Wait inside StopAllAndWait.
func TestStartAfterStopIsNoOp(t *testing.T) {
	restore := kube.SetBinaryForTest(stubKubectl(t))
	defer restore()

	m := New(nil, nil)
	m.StopAllAndWait() // manager is now terminal

	f := testForward()
	if err := m.Start(f); err != nil {
		t.Fatalf("Start returned an error instead of a silent no-op: %v", err)
	}
	if m.Active(f.ID) {
		t.Fatal("Start spun up a forward after StopAll — manager is not terminal")
	}
}

// TestStopAllAndWaitNoForwards is a no-op fast path: with nothing running it
// must return immediately rather than block.
func TestStopAllAndWaitNoForwards(t *testing.T) {
	m := New(nil, nil)
	done := make(chan struct{})
	go func() {
		m.StopAllAndWait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAllAndWait blocked with no active forwards")
	}
}
