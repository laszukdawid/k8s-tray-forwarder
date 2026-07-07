package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// dragHandle is the grip shown at the left of a forward row. Dragging it and
// releasing over a group header (re)assigns that forward to the group; the
// forward manager and config are untouched until the drop lands on a group.
type dragHandle struct {
	widget.BaseWidget
	app *App
	id  string // forward ID this handle moves
}

func newDragHandle(a *App, id string) *dragHandle {
	h := &dragHandle{app: a, id: id}
	h.ExtendBaseWidget(h)
	return h
}

func (h *dragHandle) CreateRenderer() fyne.WidgetRenderer {
	icon := widget.NewIcon(theme.MoreVerticalIcon())
	return widget.NewSimpleRenderer(icon)
}

// Dragged fires continuously while the handle is dragged; we track the group
// under the pointer and float a hint badge beside the cursor.
func (h *dragHandle) Dragged(e *fyne.DragEvent) { h.app.onForwardDragged(h.id, e.AbsolutePosition) }

// DragEnd commits the move if the pointer was released over a group header.
func (h *dragHandle) DragEnd() { h.app.onForwardDropped(h.id) }

// Cursor hints that the handle is grabbable.
func (h *dragHandle) Cursor() desktop.Cursor { return desktop.PointerCursor }

// buildDragLayer creates the transparent overlay that carries the floating
// "→ group" hint badge shown while dragging. It sits above the Manage content
// in a Stack; the badge is hidden until a drag starts.
func (a *App) buildDragLayer() fyne.CanvasObject {
	a.dragBadgeLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	bg := canvas.NewRectangle(accent)
	bg.CornerRadius = 6
	a.dragBadge = container.NewStack(bg, container.NewPadded(a.dragBadgeLabel))
	a.dragBadge.Hide()
	a.dragLayer = container.NewWithoutLayout(a.dragBadge)
	return a.dragLayer
}

// groupAt returns the name of the group header whose on-screen bounds contain
// the given absolute (canvas-relative) position, or "" if none.
func (a *App) groupAt(abs fyne.Position) string {
	drv := a.fyneApp.Driver()
	for _, z := range a.groupZones {
		pos := drv.AbsolutePositionForObject(z.obj)
		sz := z.obj.Size()
		if abs.X >= pos.X && abs.X <= pos.X+sz.Width && abs.Y >= pos.Y && abs.Y <= pos.Y+sz.Height {
			return z.name
		}
	}
	return ""
}

// onForwardDragged updates the current drop target and repositions the hint
// badge next to the cursor. Called on every drag step.
func (a *App) onForwardDragged(id string, abs fyne.Position) {
	if a.dragLayer == nil {
		return
	}
	a.dragOverGroup = a.groupAt(abs)

	label := "Drop on a group"
	if a.dragOverGroup != "" {
		label = "→  " + a.dragOverGroup
	}
	a.dragBadgeLabel.SetText(label)
	a.dragBadge.Resize(a.dragBadge.MinSize())
	a.dragBadge.Move(fyne.NewPos(abs.X+14, abs.Y+10))
	a.dragBadge.Show()
	a.dragLayer.Refresh()
}

// onForwardDropped commits the move when a drag ends over a group header. It is
// a no-op when the pointer was not over any group or the forward is already in
// that group.
func (a *App) onForwardDropped(id string) {
	target := a.dragOverGroup
	a.dragOverGroup = ""
	if a.dragBadge != nil {
		a.dragBadge.Hide()
		a.dragLayer.Refresh()
	}
	if target == "" {
		return
	}
	f, ok := a.cfg.Get(id)
	if !ok || strings.TrimSpace(f.Group) == target {
		return
	}
	f.Group = target
	if _, err := a.cfg.Upsert(f); err != nil {
		a.logf("move to group failed: %v", err)
		return
	}
	a.expandedGroups[target] = true // reveal the destination so the moved row is visible
	a.logf("moved %q into group %q", f.Name, target)
	a.onForwardChange()
}
