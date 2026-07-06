// Package ui builds the Fyne system-tray application: the tray menu with one
// toggle per forward, plus the Add/Edit and Manage windows.
package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/forward"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/loginitem"
)

const appID = "com.github.dawidlaszuk.k8s-tray-forwarder"

// App holds the shared state wired into every window and the tray.
type App struct {
	fyneApp fyne.App
	desk    desktop.App
	cfg     *config.Config
	mgr     *forward.Manager

	// keepAlive is a persistent (initially hidden) window. A desktop Fyne app
	// needs at least one window to keep its run loop alive; we reuse it as the
	// Manage window so there is no throwaway placeholder.
	keepAlive fyne.Window
	addWin    fyne.Window

	launchCheck   *widget.Check // "Launch at login" toggle in the Manage window
	launchSyncing bool          // reentrancy guard while reverting launchCheck

	// expandedGroups records which groups are expanded in the Manage window,
	// keyed by group name. Groups start collapsed (showing just the group-level
	// toggle) and expand to reveal their individual forwards.
	expandedGroups map[string]bool

	logMu    sync.Mutex
	logLines []string
	logView  func() // refresh hook for the log pane, set when Manage is built
}

// compactTheme trims the default theme's padding so the Manage window (and the
// forms) pack noticeably more into the same space without changing colours,
// fonts or icons.
type compactTheme struct{ fyne.Theme }

func (c compactTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInnerPadding:
		return 4 // default 8 — the biggest contributor to label/row height
	case theme.SizeNamePadding:
		return 3 // default 4 — gap between stacked widgets
	}
	return c.Theme.Size(name)
}

// NewApp constructs the application around an already-loaded config.
func NewApp(cfg *config.Config) (*App, error) {
	fyneApp := app.NewWithID(appID)
	fyneApp.Settings().SetTheme(compactTheme{theme.DefaultTheme()})
	desk, ok := fyneApp.(desktop.App)
	if !ok {
		return nil, fmt.Errorf("system tray is not supported on this platform")
	}
	a := &App{fyneApp: fyneApp, desk: desk, cfg: cfg, expandedGroups: map[string]bool{}}
	a.mgr = forward.New(a.onForwardChange, a.logf)
	return a, nil
}

// Run builds the tray, auto-starts flagged forwards and blocks until quit.
func (a *App) Run() {
	a.buildManageWindow() // creates the hidden keep-alive window
	a.rebuildTray()
	a.desk.SetSystemTrayIcon(theme.ComputerIcon())

	// Reconcile the macOS login item with the saved preference.
	if err := loginitem.Sync(a.cfg.LaunchAtLoginEnabled()); err != nil {
		a.logf("launch-at-login sync failed: %v", err)
	}

	for _, f := range a.cfg.List() {
		if f.AutoStart {
			if err := a.mgr.Start(f); err != nil {
				a.logf("autostart %s failed: %v", f.Name, err)
			}
		}
	}

	a.fyneApp.Run()
}

// onForwardChange is invoked from manager goroutines; marshal UI work onto the
// main thread, which Fyne requires.
func (a *App) onForwardChange() {
	fyne.Do(func() {
		a.rebuildTray()
		if a.logView != nil {
			a.logView()
		}
	})
}

// toggle flips a forward on or off from the tray.
func (a *App) toggle(f config.Forward) {
	if a.mgr.Active(f.ID) {
		a.mgr.Stop(f.ID)
		return
	}
	if err := a.mgr.Start(f); err != nil {
		a.logf("start %s failed: %v", f.Name, err)
	}
}

// toggleGroup switches a whole group on or off. If every forward in the group
// is already active it stops them all; otherwise it starts the ones that are
// not yet running (already-active ones are left untouched).
func (a *App) toggleGroup(forwards []config.Forward) {
	stop := a.groupAllActive(forwards)
	for _, f := range forwards {
		switch {
		case stop:
			a.mgr.Stop(f.ID)
		case !a.mgr.Active(f.ID):
			if err := a.mgr.Start(f); err != nil {
				a.logf("start %s failed: %v", f.Name, err)
			}
		}
	}
}

// groupAllActive reports whether every forward in a non-empty group is active.
func (a *App) groupAllActive(forwards []config.Forward) bool {
	for _, f := range forwards {
		if !a.mgr.Active(f.ID) {
			return false
		}
	}
	return len(forwards) > 0
}

// groupSummary aggregates a group's live state into a single status glyph plus
// the running/total counts shown next to the group name.
func (a *App) groupSummary(forwards []config.Forward) (glyph string, running, total int) {
	total = len(forwards)
	var anyErr, anyPending bool
	for _, f := range forwards {
		switch a.mgr.Status(f.ID).State {
		case forward.StateRunning:
			running++
		case forward.StateError:
			anyErr = true
		case forward.StateStarting, forward.StateReconnect:
			anyPending = true
		}
	}
	switch {
	case anyErr:
		glyph = "⚠"
	case anyPending:
		glyph = "⟳"
	case total > 0 && running == total:
		glyph = "●"
	case running > 0:
		glyph = "◐"
	default:
		glyph = "○"
	}
	return glyph, running, total
}

// reloadConfig re-reads the file from disk so hand-edits take effect, leaving
// already-running forwards untouched.
func (a *App) reloadConfig() {
	cfg, err := config.Load(a.cfg.Path())
	if err != nil {
		a.logf("reload failed: %v", err)
		return
	}
	a.cfg = cfg
	a.logf("config reloaded from %s", a.cfg.Path())
	a.onForwardChange()
}

// openConfigInEditor reveals the config file using the OS default handler.
func (a *App) openConfigInEditor() {
	path := a.cfg.Path()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		a.logf("open config failed: %v", err)
	}
}

// setLaunchAtLogin installs/removes the LaunchAgent first and persists the
// preference only if that succeeds, so the saved config never disagrees with
// the actual login-item state. On failure the checkbox is reverted.
func (a *App) setLaunchAtLogin(v bool) {
	if a.launchSyncing {
		return // ignore the OnChanged fired by our own revert below
	}
	if err := loginitem.Sync(v); err != nil {
		a.logf("launch-at-login: %v", err)
		a.revertLaunchCheck()
		return
	}
	if err := a.cfg.SetLaunchAtLogin(v); err != nil {
		a.logf("save launch-at-login failed: %v", err)
		a.revertLaunchCheck()
		return
	}
	if v {
		a.logf("launch at login enabled (takes effect next login)")
	} else {
		a.logf("launch at login disabled")
	}
}

// revertLaunchCheck restores the checkbox to the persisted preference without
// re-triggering setLaunchAtLogin.
func (a *App) revertLaunchCheck() {
	if a.launchCheck == nil {
		return
	}
	a.launchSyncing = true
	a.launchCheck.SetChecked(a.cfg.LaunchAtLoginEnabled())
	a.launchSyncing = false
}

func (a *App) quit() {
	a.mgr.StopAllAndWait()
	a.fyneApp.Quit()
}

// logf appends a timestamped line to the in-app log ring buffer.
func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	a.logMu.Lock()
	a.logLines = append(a.logLines, line)
	if len(a.logLines) > 500 {
		a.logLines = a.logLines[len(a.logLines)-500:]
	}
	a.logMu.Unlock()
}

func (a *App) logText() string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := ""
	for _, l := range a.logLines {
		out += l + "\n"
	}
	return out
}
