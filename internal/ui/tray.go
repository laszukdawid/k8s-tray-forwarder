package ui

import (
	"fmt"

	"fyne.io/fyne/v2"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/forward"
)

// rebuildTray reconstructs the whole tray menu from current config + state.
// Fyne has no incremental menu update, so we replace the menu wholesale; this
// is cheap and keeps checkmarks/labels perfectly in sync with status.
func (a *App) rebuildTray() {
	var items []*fyne.MenuItem

	forwards := a.cfg.List()
	if len(forwards) == 0 {
		empty := fyne.NewMenuItem("No forwards configured", nil)
		empty.Disabled = true
		items = append(items, empty)
	}
	for _, f := range forwards {
		f := f
		st := a.mgr.Status(f.ID)
		item := fyne.NewMenuItem(menuLabel(f, st), func() { a.toggle(f) })
		item.Checked = a.mgr.Active(f.ID)
		items = append(items, item)
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Add Forward…", func() { a.showAddWindow(nil) }),
		fyne.NewMenuItem("Manage Forwards…", func() { a.showManageWindow() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Reload Config", a.reloadConfig),
		fyne.NewMenuItem("Open Config File…", a.openConfigInEditor),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", a.quit),
	)

	a.desk.SetSystemTrayMenu(fyne.NewMenu("K8s Port Forwards", items...))
}

// menuLabel renders a status glyph + name + connection summary.
func menuLabel(f config.Forward, st forward.Status) string {
	glyph := "○"
	switch st.State {
	case forward.StateRunning:
		glyph = "●"
	case forward.StateStarting, forward.StateReconnect:
		glyph = "⟳"
	case forward.StateError:
		glyph = "⚠"
	}
	switch st.State {
	case forward.StateRunning:
		return fmt.Sprintf("%s  %s  →  %s:%d", glyph, f.Name, f.BindAddress(), f.LocalPort)
	case forward.StateError:
		return fmt.Sprintf("%s  %s  (retrying)", glyph, f.Name)
	case forward.StateStarting:
		return fmt.Sprintf("%s  %s  (starting…)", glyph, f.Name)
	case forward.StateReconnect:
		return fmt.Sprintf("%s  %s  (reconnecting…)", glyph, f.Name)
	default:
		return fmt.Sprintf("%s  %s", glyph, f.Name)
	}
}
