package ui

import (
	"fmt"
	"image/color"

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
	glyph, _, running, total := a.groupSummary(forwards)

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

// glyph is the monochrome status marker used in the (OS-drawn) tray menu, where
// we can't use a coloured dot.
func glyph(state forward.State) string {
	switch state {
	case forward.StateRunning:
		return "●"
	case forward.StateStarting, forward.StateReconnect:
		return "⟳"
	case forward.StateError:
		return "⚠"
	default:
		return "○"
	}
}

// statusColor maps a forward's state to the dot colour shown in the Manage
// window: green running, amber starting/reconnecting, red error, grey stopped.
func statusColor(state forward.State) color.Color {
	switch state {
	case forward.StateRunning:
		return color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF}
	case forward.StateStarting, forward.StateReconnect:
		return color.NRGBA{R: 0xF5, G: 0x9E, B: 0x0B, A: 0xFF}
	case forward.StateError:
		return color.NRGBA{R: 0xEF, G: 0x44, B: 0x44, A: 0xFF}
	default:
		return color.NRGBA{R: 0x9C, G: 0xA3, B: 0xAF, A: 0xFF}
	}
}

// statusText is the name + connection summary shown after the status marker.
func statusText(f config.Forward, st forward.Status) string {
	switch st.State {
	case forward.StateRunning:
		return fmt.Sprintf("%s  →  %s:%d", f.Name, f.BindAddress(), f.LocalPort)
	case forward.StateError:
		return f.Name + "  (retrying)"
	case forward.StateStarting:
		return f.Name + "  (starting…)"
	case forward.StateReconnect:
		return f.Name + "  (reconnecting…)"
	default:
		return f.Name
	}
}

// menuLabel renders a status glyph + name + connection summary for the tray.
func menuLabel(f config.Forward, st forward.Status) string {
	return glyph(st.State) + "  " + statusText(f, st)
}
