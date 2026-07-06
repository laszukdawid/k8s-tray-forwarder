package ui

import (
	"context"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/forward"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/kube"
)

// showAddWindow opens the Add/Edit window. Pass nil to create a new forward or
// an existing forward to edit it. Only one such window exists at a time.
func (a *App) showAddWindow(existing *config.Forward) {
	if a.addWin != nil {
		a.addWin.Close()
	}
	title := "Add Forward"
	if existing != nil {
		title = "Edit Forward — " + existing.Name
	}
	win := a.fyneApp.NewWindow(title)
	a.addWin = win
	// Only clear the reference if it still points at *this* window — otherwise a
	// freshly opened Add window (which replaced this one) would be orphaned.
	win.SetOnClosed(func() {
		if a.addWin == win {
			a.addWin = nil
		}
	})

	// --- widgets ------------------------------------------------------------
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("e.g. Postgres")

	groupEntry := widget.NewSelectEntry(a.existingGroups())
	groupEntry.SetPlaceHolder("Optional — cluster with related forwards")

	contextSelect := widget.NewSelect(nil, nil)
	contextSelect.PlaceHolder = "Select context…"

	namespaceSelect := widget.NewSelect(nil, nil)
	namespaceSelect.PlaceHolder = "Select namespace…"

	kindSelect := widget.NewSelect(
		[]string{config.KindDeployment, config.KindService, config.KindPod}, nil)
	kindSelect.SetSelected(config.KindDeployment)

	targetSelect := widget.NewSelectEntry(nil)
	targetSelect.SetPlaceHolder("Pick or type a name…")

	remoteEntry := widget.NewEntry()
	remoteEntry.SetPlaceHolder("4000")
	localEntry := widget.NewEntry()
	localEntry.SetPlaceHolder("same as remote")
	addressEntry := widget.NewEntry()
	addressEntry.SetPlaceHolder("127.0.0.1")
	autostartCheck := widget.NewCheck("Start automatically on launch", nil)

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	// A long auth failure (e.g. a stale AWS SSO session) produces a very tall
	// error message. Keep it inside a height-bounded scroll: without this the
	// label grows the window's MinSize until the window outgrows the screen and
	// buries the Cancel/Save buttons, leaving no way to dismiss it. The user can
	// scroll to read the full text and clear it with the dismiss (✕) button.
	statusScroll := container.NewVScroll(status)
	dismissBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), nil)
	dismissBtn.Importance = widget.LowImportance
	// Wrap the dismiss button so it keeps its natural height instead of stretching
	// to the (taller) status scroll beside it. Toggle visibility on the wrapper,
	// not the button: a hidden wrapper makes the border's right region collapse
	// entirely (no padding reserved), so the scroll reclaims the full width.
	dismissBox := container.NewVBox(dismissBtn)
	dismissBox.Hide()

	// resources holds the most recently fetched targets so we can prefill ports.
	var resources []kube.Resource
	// Monotonic request counters: each loader bumps its counter and captures the
	// value; a late async result whose counter no longer matches is discarded, so
	// a slow response for a stale selection can't clobber a newer one. All reads
	// and writes happen on the main goroutine (OnChanged + fyne.Do callbacks).
	var nsReq, resReq int

	setStatus := func(s string) {
		status.SetText(s)
		statusScroll.SetMinSize(fyne.Size{}) // collapse back to the default one-line height
		statusScroll.Refresh()
		dismissBox.Hide()
	}
	// setError surfaces err in the status area with enough room to read it plus a
	// dismiss button, and records the full text in the activity log so a long
	// message stays available after it is cleared.
	setError := func(err error) {
		status.SetText("error: " + err.Error())
		statusScroll.SetMinSize(fyne.NewSize(0, 96)) // capped so the window stays on-screen
		statusScroll.ScrollToTop()
		statusScroll.Refresh()
		dismissBox.Show()
		a.logf("forward setup: %v", err)
	}
	dismissBtn.OnTapped = func() { setStatus("") }

	// --- async loaders ------------------------------------------------------
	// Discovery shells out to kubectl — Namespaces and Resources hit the cluster
	// (and its auth), so it runs only on demand: the per-field ↻ buttons, or the
	// cascade below when the user changes an upstream selection. Editing an
	// existing forward pre-fills every field from the saved values and triggers
	// none of these, so opening Edit is instant and needs no cluster access.
	// Each loader preserves the current selection (via ensureOption) so a refresh
	// widens the choices without discarding what is already picked.
	loadContexts := func() {
		setStatus("loading contexts…")
		go func() {
			ctxs, err := kube.Contexts(context.Background())
			fyne.Do(func() {
				if err != nil {
					setError(err)
					return
				}
				contextSelect.Options = ensureOption(ctxs, contextSelect.Selected)
				contextSelect.Refresh()
				setStatus(fmt.Sprintf("%d context(s)", len(ctxs)))
			})
		}()
	}

	loadNamespaces := func() {
		ctxName := contextSelect.Selected
		if ctxName == "" {
			setStatus("pick a context first")
			return
		}
		nsReq++
		req := nsReq
		setStatus("loading namespaces…")
		go func() {
			nss, err := kube.Namespaces(context.Background(), ctxName)
			fyne.Do(func() {
				if req != nsReq {
					return // superseded by a newer request
				}
				if err != nil {
					setError(err)
					return
				}
				namespaceSelect.Options = ensureOption(nss, namespaceSelect.Selected)
				namespaceSelect.Refresh()
				setStatus(fmt.Sprintf("%d namespace(s)", len(nss)))
			})
		}()
	}

	loadResources := func() {
		ctxName, ns, kind := contextSelect.Selected, namespaceSelect.Selected, kindSelect.Selected
		if ctxName == "" || ns == "" || kind == "" {
			setStatus("pick a context, namespace and kind first")
			return
		}
		resReq++
		req := resReq
		setStatus(fmt.Sprintf("loading %ss in %s…", kind, ns))
		go func() {
			res, err := kube.Resources(context.Background(), ctxName, ns, kind)
			fyne.Do(func() {
				if req != resReq {
					return // superseded by a newer request
				}
				if err != nil {
					setError(err)
					return
				}
				resources = res
				names := make([]string, len(res))
				for i, r := range res {
					names[i] = r.Name
				}
				targetSelect.SetOptions(ensureOption(names, targetSelect.Text))
				setStatus(fmt.Sprintf("%d %s(s) found", len(res), kind))
			})
		}()
	}

	// --- wire cascade -------------------------------------------------------
	// These fire only on a user-initiated change (seeding the selects for an edit
	// sets their .Selected field directly, which does not invoke OnChanged).
	// Picking a different context or namespace is deliberate, so re-querying the
	// cluster for the now-stale downstream choices is expected there.
	contextSelect.OnChanged = func(string) {
		resReq++ // invalidate any in-flight resource load tied to the old context
		namespaceSelect.ClearSelected()
		namespaceSelect.Options = nil
		namespaceSelect.Refresh()
		targetSelect.SetOptions(nil)
		targetSelect.SetText("")
		resources = nil
		loadNamespaces()
	}
	namespaceSelect.OnChanged = func(string) {
		targetSelect.SetText("")
		loadResources()
	}
	kindSelect.OnChanged = func(string) {
		targetSelect.SetText("")
		loadResources()
	}
	targetSelect.OnChanged = func(name string) {
		// Prefill the remote port from the discovered container/service port,
		// but never clobber a value the user already typed.
		if strings.TrimSpace(remoteEntry.Text) != "" {
			return
		}
		for _, r := range resources {
			if r.Name == name && len(r.Ports) > 0 {
				remoteEntry.SetText(strconv.Itoa(r.Ports[0]))
				return
			}
		}
	}

	// --- initial population -------------------------------------------------
	if existing != nil {
		// Edit: every field is already known, so fill it straight from the saved
		// forward and touch the cluster for nothing. The selects' values are set
		// directly (not via SetSelected) so seeding them doesn't fire OnChanged
		// and kick off the discovery cascade. Each select is seeded with just its
		// current value as the sole option; the ↻ buttons load the live choices
		// only when the user actually wants to change one.
		nameEntry.SetText(existing.Name)
		groupEntry.SetText(existing.Group)
		if existing.RemotePort > 0 {
			remoteEntry.SetText(strconv.Itoa(existing.RemotePort))
		}
		if existing.LocalPort > 0 {
			localEntry.SetText(strconv.Itoa(existing.LocalPort))
		}
		if existing.Address != "" {
			addressEntry.SetText(existing.Address)
		}
		autostartCheck.SetChecked(existing.AutoStart)

		contextSelect.Options = []string{existing.Context}
		contextSelect.Selected = existing.Context
		contextSelect.Refresh()
		namespaceSelect.Options = []string{existing.Namespace}
		namespaceSelect.Selected = existing.Namespace
		namespaceSelect.Refresh()
		kindSelect.Selected = orDefault(existing.TargetKind, config.KindDeployment)
		kindSelect.Refresh()
		targetSelect.SetOptions([]string{existing.TargetName})
		targetSelect.SetText(existing.TargetName)
	} else {
		// Add: nothing is known yet, so load the (local, cheap) context list to
		// start the pick-context → namespace → target cascade.
		loadContexts()
	}

	// --- save ---------------------------------------------------------------
	save := func() {
		remote, err := strconv.Atoi(strings.TrimSpace(remoteEntry.Text))
		if err != nil {
			dialog.ShowError(fmt.Errorf("remote port must be a number"), win)
			return
		}
		local := remote
		if s := strings.TrimSpace(localEntry.Text); s != "" {
			if local, err = strconv.Atoi(s); err != nil {
				dialog.ShowError(fmt.Errorf("local port must be a number"), win)
				return
			}
		}
		fwd := config.Forward{
			Type:       config.TypeKubernetes,
			Name:       strings.TrimSpace(nameEntry.Text),
			Group:      strings.TrimSpace(groupEntry.Text),
			Context:    contextSelect.Selected,
			Namespace:  namespaceSelect.Selected,
			TargetKind: kindSelect.Selected,
			TargetName: strings.TrimSpace(targetSelect.Text),
			RemotePort: remote,
			LocalPort:  local,
			Address:    strings.TrimSpace(addressEntry.Text),
			AutoStart:  autostartCheck.Checked,
		}
		if existing != nil {
			fwd.ID = existing.ID
		}
		if err := fwd.Validate(); err != nil {
			dialog.ShowError(err, win)
			return
		}
		saved, err := a.cfg.Upsert(fwd)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		// If the forward was running, restart it so changes take effect.
		if a.mgr.Active(saved.ID) {
			a.mgr.Stop(saved.ID)
			_ = a.mgr.Start(saved)
		}
		// Reveal the group the forward now belongs to, so a freshly created or
		// re-assigned forward is visible instead of hidden inside a collapsed
		// group. Only when the group is new/changed, so editing an unrelated
		// field doesn't force a group the user deliberately collapsed back open.
		if saved.Group != "" && (existing == nil || existing.Group != saved.Group) {
			a.expandedGroups[saved.Group] = true
		}
		a.logf("saved forward %q", saved.Name)
		a.onForwardChange()
		win.Close()
	}

	// withReload pairs a cluster-backed select with a ↻ button that fetches its
	// live choices on demand, so editing an existing forward never has to reload
	// the field just to display the value it already holds.
	withReload := func(field fyne.CanvasObject, reload func()) fyne.CanvasObject {
		btn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), reload)
		btn.Importance = widget.LowImportance
		return container.NewBorder(nil, nil, nil, btn, field)
	}

	form := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Group", groupEntry),
		widget.NewFormItem("Context", withReload(contextSelect, loadContexts)),
		widget.NewFormItem("Namespace", withReload(namespaceSelect, loadNamespaces)),
		widget.NewFormItem("Target kind", kindSelect),
		widget.NewFormItem("Target", withReload(targetSelect, loadResources)),
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Local port", localEntry),
		widget.NewFormItem("Bind address", addressEntry),
		widget.NewFormItem("", autostartCheck),
	)

	buttons := container.NewHBox(
		widget.NewButton("Cancel", func() { win.Close() }),
		widget.NewButton("Save", save),
	)

	statusRow := container.NewBorder(nil, nil, nil, dismissBox, statusScroll)

	content := container.NewBorder(
		widget.NewLabelWithStyle("Kubernetes port-forward", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewVBox(statusRow, buttons),
		nil, nil,
		container.NewVScroll(form),
	)
	win.SetContent(container.NewPadded(content))
	win.Resize(fyne.NewSize(520, 600))
	win.Show()
	win.RequestFocus()
}

// buildManageWindow creates the persistent (hidden) Manage window. It doubles
// as the keep-alive window for the run loop. Closing it hides instead of
// destroying, so it can be reopened from the tray.
func (a *App) buildManageWindow() {
	win := a.fyneApp.NewWindow("Manage Forwards")
	a.keepAlive = win
	win.SetCloseIntercept(func() { win.Hide() })

	rows := container.NewVBox()
	logEntry := widget.NewMultiLineEntry()
	logEntry.Wrapping = fyne.TextWrapWord

	refresh := func() {
		rows.Objects = a.manageRows()
		rows.Refresh()
		logEntry.SetText(a.logText())
		logEntry.CursorRow = strings.Count(logEntry.Text, "\n")
	}
	a.logView = refresh

	a.launchCheck = widget.NewCheck("Launch at login", func(v bool) { a.setLaunchAtLogin(v) })
	// Set the initial state without firing setLaunchAtLogin (already synced at startup).
	a.launchSyncing = true
	a.launchCheck.SetChecked(a.cfg.LaunchAtLoginEnabled())
	a.launchSyncing = false
	header := container.NewHBox(
		widget.NewButton("Add Forward…", func() { a.showAddWindow(nil) }),
		widget.NewButton("Reload Config", func() { a.reloadConfig() }),
		a.launchCheck,
	)
	logBox := container.NewBorder(
		widget.NewLabelWithStyle("Activity", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		container.NewVScroll(logEntry),
	)
	split := container.NewVSplit(container.NewVScroll(rows), logBox)
	split.Offset = 0.6

	win.SetContent(container.NewBorder(header, nil, nil, nil, split))
	win.Resize(fyne.NewSize(660, 600))
	refresh()
}

// showManageWindow reveals and refreshes the Manage window.
func (a *App) showManageWindow() {
	if a.logView != nil {
		a.logView()
	}
	a.keepAlive.Show()
	a.keepAlive.RequestFocus()
}

// manageRows builds the Manage window body: one card per ungrouped forward and
// a collapsible header (plus indented child cards when expanded) per group.
func (a *App) manageRows() []fyne.CanvasObject {
	groups := config.GroupForwards(a.cfg.List())
	if len(groups) == 0 {
		return []fyne.CanvasObject{widget.NewLabel("No forwards yet. Click “Add Forward…”.")}
	}
	var objs []fyne.CanvasObject
	for _, g := range groups {
		if g.Name == "" {
			objs = append(objs, a.forwardCard(g.Forwards[0], false), widget.NewSeparator())
			continue
		}
		objs = append(objs, a.groupRows(g)...)
	}
	return objs
}

// groupRows builds a group's single-line collapsible header followed by its
// indented child cards when the group is expanded.
func (a *App) groupRows(g config.ForwardGroup) []fyne.CanvasObject {
	name := g.Name
	expanded := a.expandedGroups[name]
	glyph, running, total := a.groupSummary(g.Forwards)

	expandIcon := theme.MenuExpandIcon()
	if expanded {
		expandIcon = theme.MenuDropDownIcon()
	}
	expandBtn := widget.NewButtonWithIcon("", expandIcon, func() {
		a.expandedGroups[name] = !a.expandedGroups[name]
		if a.logView != nil {
			a.logView()
		}
	})
	expandBtn.Importance = widget.LowImportance

	title := widget.NewLabelWithStyle(
		fmt.Sprintf("%s  %s   (%d/%d)", glyph, name, running, total),
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis

	toggleText := "Start all"
	if a.groupAllActive(g.Forwards) {
		toggleText = "Stop all"
	}
	forwards := g.Forwards
	toggleBtn := widget.NewButton(toggleText, func() { a.toggleGroup(forwards) })
	toggleBtn.Importance = widget.LowImportance

	header := container.NewBorder(nil, nil, expandBtn, vCenter(toggleBtn), title)
	objs := []fyne.CanvasObject{header}
	if expanded {
		for _, f := range g.Forwards {
			objs = append(objs, a.forwardCard(f, true))
		}
	}
	return append(objs, widget.NewSeparator())
}

// forwardCard builds a compact two-line row for a single forward: a bold status
// line and a muted connection summary on the left, with small icon buttons
// (Start/Stop, Edit, Delete) on the right. When indented is true the card is
// offset so it reads as a child of its group header. Both text lines truncate
// with an ellipsis rather than wrapping, so every row stays one fixed height.
func (a *App) forwardCard(f config.Forward, indented bool) fyne.CanvasObject {
	st := a.mgr.Status(f.ID)
	active := a.mgr.Active(f.ID)

	title := widget.NewLabelWithStyle(menuLabel(f, st), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis

	detail := f.Context + " · " + f.Namespace + " · " + f.TargetKind + "/" + f.TargetName +
		fmt.Sprintf(" · %d→%d", f.LocalPort, f.RemotePort)
	if st.State == forward.StateError && st.LastErr != "" {
		detail += "   ⚠ " + st.LastErr
	}
	detailLabel := widget.NewLabel(detail)
	detailLabel.Truncation = fyne.TextTruncateEllipsis

	toggleIcon := theme.MediaPlayIcon()
	if active {
		toggleIcon = theme.MediaStopIcon()
	}
	toggleBtn := widget.NewButtonWithIcon("", toggleIcon, func() { a.toggle(f) })
	toggleBtn.Importance = widget.LowImportance
	editBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		cur, _ := a.cfg.Get(f.ID)
		a.showAddWindow(&cur)
	})
	editBtn.Importance = widget.LowImportance
	delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete forward", "Delete \""+f.Name+"\"?", func(ok bool) {
			if !ok {
				return
			}
			a.mgr.Stop(f.ID)
			if err := a.cfg.Delete(f.ID); err != nil {
				a.logf("delete failed: %v", err)
			}
			a.onForwardChange()
		}, a.keepAlive)
	})
	delBtn.Importance = widget.LowImportance

	controls := vCenter(container.NewHBox(toggleBtn, editBtn, delBtn))
	body := container.NewVBox(title, detailLabel)
	card := container.NewBorder(nil, nil, nil, controls, body)
	if indented {
		return container.NewBorder(nil, nil, indentSpacer(), nil, card)
	}
	return card
}

// vCenter keeps an object at its natural height, centered vertically, so it does
// not stretch to fill the taller cell beside it in a Border layout.
func vCenter(o fyne.CanvasObject) fyne.CanvasObject { return container.NewCenter(o) }

// indentSpacer is a fixed-width, zero-height transparent gap used to offset a
// group's child rows without inflating their height (as a padded label would).
func indentSpacer() fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(22, 0))
	return r
}

// existingGroups returns the distinct group names already in use, sorted, to
// offer as suggestions when adding or editing a forward.
func (a *App) existingGroups() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range a.cfg.List() {
		if g := strings.TrimSpace(f.Group); g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

func ensureOption(opts []string, v string) []string {
	if v == "" {
		return opts
	}
	for _, o := range opts {
		if o == v {
			return opts
		}
	}
	return append(opts, v)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
