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

	groups := config.GroupForwards(a.cfg.List())
	if len(groups) == 0 {
		empty := fyne.NewMenuItem("No forwards configured", nil)
		empty.Disabled = true
		items = append(items, empty)
	}
	for _, g := range groups {
		if g.Name == "" {
			items = append(items, a.forwardMenuItem(g.Forwards[0]))
			continue
		}
		items = append(items, a.groupMenuItem(g))
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

// forwardMenuItem builds a single toggleable tray entry for one forward.
func (a *App) forwardMenuItem(f config.Forward) *fyne.MenuItem {
	st := a.mgr.Status(f.ID)
	item := fyne.NewMenuItem(menuLabel(f, st), func() { a.toggle(f) })
	item.Checked = a.mgr.Active(f.ID)
	return item
}

// groupMenuItem builds a submenu for a group: hovering the parent expands the
// list of individual forwards (each toggleable), and a "Start all"/"Stop all"
// entry at the top flips the whole group at once. The parent label carries an
// aggregate status glyph and a running/total count.
func (a *App) groupMenuItem(g config.ForwardGroup) *fyne.MenuItem {
	forwards := g.Forwards
	glyph, running, total := a.groupSummary(forwards)

	toggleText := "Start all"
	if a.groupAllActive(forwards) {
		toggleText = "Stop all"
	}
	children := []*fyne.MenuItem{
		fyne.NewMenuItem(toggleText, func() { a.toggleGroup(forwards) }),
		fyne.NewMenuItemSeparator(),
	}
	for _, f := range forwards {
		children = append(children, a.forwardMenuItem(f))
	}

	parent := fyne.NewMenuItem(fmt.Sprintf("%s  %s  (%d/%d)", glyph, g.Name, running, total), nil)
	parent.ChildMenu = fyne.NewMenu("", children...)
	return parent
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
