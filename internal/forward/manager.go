// Package forward runs and supervises kubectl port-forward processes. Each
// active forward gets a goroutine that (re)launches kubectl and watches its
// output, transitioning through a small state machine and auto-reconnecting
// with backoff when the process exits unexpectedly (pod rescheduled, token
// refresh, transient network blip).
package forward

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/kube"
)

// State is the observable status of a forward.
type State string

const (
	StateStopped   State = "stopped"
	StateStarting  State = "starting"
	StateRunning   State = "running"
	StateReconnect State = "reconnecting"
	StateError     State = "error"
)

const (
	minBackoff = 1 * time.Second
	maxBackoff = 15 * time.Second
)

// Status is an immutable snapshot of one forward's runtime state.
type Status struct {
	State   State
	LastErr string
	Detail  string // e.g. "127.0.0.1:4000 -> 4000"
	Forward config.Forward
}

type session struct {
	fwd    config.Forward
	cancel context.CancelFunc
	status Status
}

// Manager owns all running forwards. It is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	wg       sync.WaitGroup // tracks live supervisor goroutines for clean shutdown
	onChange func()
	logf     func(format string, args ...any)
}

// New creates a manager. onChange is invoked (from a background goroutine)
// whenever any forward's status changes, so the UI can refresh. logf receives
// human-readable lifecycle messages.
func New(onChange func(), logf func(string, ...any)) *Manager {
	if onChange == nil {
		onChange = func() {}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Manager{
		sessions: map[string]*session{},
		onChange: onChange,
		logf:     logf,
	}
}

// Active reports whether the user has switched this forward on (regardless of
// whether the underlying process is momentarily reconnecting).
func (m *Manager) Active(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[id]
	return ok
}

// Status returns the current snapshot for a forward, or a stopped status if it
// is not active.
func (m *Manager) Status(id string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s.status
	}
	return Status{State: StateStopped}
}

// Start switches a forward on. If it is already active the call is a no-op.
func (m *Manager) Start(fwd config.Forward) error {
	if err := fwd.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	if _, ok := m.sessions[fwd.ID]; ok {
		m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{fwd: fwd, cancel: cancel, status: Status{State: StateStarting, Forward: fwd}}
	m.sessions[fwd.ID] = s
	m.wg.Add(1)
	m.mu.Unlock()

	m.onChange()
	go func() {
		defer m.wg.Done()
		m.supervise(ctx, s)
	}()
	return nil
}

// Stop switches a forward off and kills its process.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		s.cancel()
		m.logf("stopped %s", s.fwd.Name)
		m.onChange()
	}
}

// StopAll tears down every active forward (used on quit).
func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := make([]*session, 0, len(m.sessions))
	for id, s := range m.sessions {
		sessions = append(sessions, s)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.cancel()
	}
}

// StopAllAndWait stops every forward and blocks until all supervisor goroutines
// have torn down their kubectl processes. Use on quit so we don't orphan
// port-forwards (which would keep holding their local ports).
func (m *Manager) StopAllAndWait() {
	m.StopAll()
	m.wg.Wait()
}

// setStatus updates a session's status iff that exact session is still the
// active one for its ID, then notifies listeners. Comparing session identity
// (not just the ID) prevents a stale goroutine — from a forward that was
// stopped and immediately restarted under the same ID — from clobbering the new
// session's status.
func (m *Manager) setStatus(sess *session, mutate func(*Status)) {
	m.mu.Lock()
	live := m.sessions[sess.fwd.ID] == sess
	if live {
		mutate(&sess.status)
	}
	m.mu.Unlock()
	if live {
		m.onChange()
	}
}

// supervise is the per-forward loop: launch kubectl, watch it, and relaunch on
// unexpected exit until the context is cancelled by Stop.
func (m *Manager) supervise(ctx context.Context, sess *session) {
	fwd := sess.fwd
	backoff := minBackoff
	first := true
	for {
		if ctx.Err() != nil {
			return
		}
		if first {
			m.setStatus(sess, func(s *Status) { s.State = StateStarting })
			m.logf("starting %s (%s/%s %s/%s %d:%d)", fwd.Name, fwd.Context, fwd.Namespace,
				fwd.TargetKind, fwd.TargetName, fwd.LocalPort, fwd.RemotePort)
		} else {
			m.setStatus(sess, func(s *Status) { s.State = StateReconnect })
		}
		first = false

		ran, err := m.runOnce(ctx, sess)
		if ctx.Err() != nil {
			return // user requested stop
		}
		if ran {
			backoff = minBackoff // we were healthy at least once; reset backoff
		}
		errMsg := "process exited"
		if err != nil {
			errMsg = err.Error()
		}
		m.setStatus(sess, func(s *Status) {
			s.State = StateError
			s.LastErr = errMsg
		})
		m.logf("%s disconnected: %s (retry in %s)", fwd.Name, errMsg, backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runOnce launches a single kubectl port-forward and blocks until it exits.
// It returns whether the forward reached the "Forwarding from" healthy state.
func (m *Manager) runOnce(ctx context.Context, sess *session) (bool, error) {
	fwd := sess.fwd
	args := []string{
		"--context", fwd.Context,
		"-n", fwd.Namespace,
		"port-forward",
		fmt.Sprintf("%s/%s", fwd.TargetKind, fwd.TargetName),
		"--address", fwd.BindAddress(),
		fmt.Sprintf("%d:%d", fwd.LocalPort, fwd.RemotePort),
	}
	cmd := exec.CommandContext(ctx, kube.Binary(), args...)
	cmd.Env = kube.Env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return false, err
	}

	// Drain stdout with a bufio.Reader (no fixed token-size limit, unlike
	// bufio.Scanner) so an unexpectedly long line can never stall the read and
	// deadlock cmd.Wait() on a full pipe.
	healthy := false
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if s := strings.TrimRight(line, "\r\n"); strings.HasPrefix(s, "Forwarding from") {
			healthy = true
			detail := strings.TrimSpace(strings.TrimPrefix(s, "Forwarding from"))
			m.setStatus(sess, func(st *Status) {
				st.State = StateRunning
				st.LastErr = ""
				st.Detail = detail
			})
			m.logf("%s ready: %s", fwd.Name, detail)
		}
		if readErr != nil {
			break // EOF (process exiting) or a read error; stop draining
		}
	}

	waitErr := cmd.Wait()
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return healthy, fmt.Errorf("%s", lastLine(msg))
	}
	return healthy, waitErr
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
