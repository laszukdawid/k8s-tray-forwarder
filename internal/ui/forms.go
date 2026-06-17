package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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

	// resources holds the most recently fetched targets so we can prefill ports.
	var resources []kube.Resource
	// want* carry edit values to be re-applied as each async load completes,
	// re-using the normal OnChanged cascade instead of duplicating it.
	var wantNamespace, wantTarget string
	// restoring is true while the edit-prefill cascade runs; it stops a
	// user-initiated change from wiping the want* slots before they are applied.
	restoring := existing != nil
	// Monotonic request counters: each loader bumps its counter and captures the
	// value; a late async result whose counter no longer matches is discarded, so
	// a slow response for a stale selection can't clobber a newer one. All reads
	// and writes happen on the main goroutine (OnChanged + fyne.Do callbacks).
	var nsReq, resReq int

	setStatus := func(s string) { status.SetText(s) }

	// --- async loaders ------------------------------------------------------
	loadResources := func() {
		ctxName, ns, kind := contextSelect.Selected, namespaceSelect.Selected, kindSelect.Selected
		if ctxName == "" || ns == "" || kind == "" {
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
				restoring = false // edit cascade has reached its final stage
				if err != nil {
					setStatus("error: " + err.Error())
					return
				}
				resources = res
				names := make([]string, len(res))
				for i, r := range res {
					names[i] = r.Name
				}
				targetSelect.SetOptions(names)
				setStatus(fmt.Sprintf("%d %s(s) found", len(res), kind))
				if wantTarget != "" {
					t := wantTarget
					wantTarget = ""
					targetSelect.SetText(t)
				}
			})
		}()
	}

	loadNamespaces := func() {
		ctxName := contextSelect.Selected
		if ctxName == "" {
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
					setStatus("error: " + err.Error())
					return
				}
				if wantNamespace != "" {
					nss = ensureOption(nss, wantNamespace)
				}
				namespaceSelect.Options = nss
				namespaceSelect.Refresh()
				setStatus("")
				if wantNamespace != "" {
					ns := wantNamespace
					wantNamespace = ""
					namespaceSelect.SetSelected(ns)
				}
			})
		}()
	}

	// --- wire cascade -------------------------------------------------------
	contextSelect.OnChanged = func(string) {
		resReq++ // invalidate any in-flight resource load tied to the old context
		if !restoring {
			wantNamespace, wantTarget = "", ""
		}
		namespaceSelect.ClearSelected()
		namespaceSelect.Options = nil
		namespaceSelect.Refresh()
		targetSelect.SetOptions(nil)
		targetSelect.SetText("")
		resources = nil
		loadNamespaces()
	}
	namespaceSelect.OnChanged = func(string) {
		if !restoring {
			wantTarget = ""
		}
		targetSelect.SetText("")
		loadResources()
	}
	kindSelect.OnChanged = func(string) {
		if !restoring {
			wantTarget = ""
		}
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

	// --- prefill when editing ----------------------------------------------
	if existing != nil {
		nameEntry.SetText(existing.Name)
		kindSelect.SetSelected(orDefault(existing.TargetKind, config.KindDeployment))
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
		wantNamespace = existing.Namespace
		wantTarget = existing.TargetName
	}

	// --- load contexts (kicks off the cascade for edits) --------------------
	setStatus("loading contexts…")
	go func() {
		ctxs, err := kube.Contexts(context.Background())
		fyne.Do(func() {
			if err != nil {
				setStatus("error: " + err.Error())
				return
			}
			if existing != nil {
				ctxs = ensureOption(ctxs, existing.Context)
			}
			contextSelect.Options = ctxs
			contextSelect.Refresh()
			setStatus("")
			if existing != nil && existing.Context != "" {
				contextSelect.SetSelected(existing.Context)
			}
		})
	}()

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
		a.logf("saved forward %q", saved.Name)
		a.onForwardChange()
		win.Close()
	}

	form := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Context", contextSelect),
		widget.NewFormItem("Namespace", namespaceSelect),
		widget.NewFormItem("Target kind", kindSelect),
		widget.NewFormItem("Target", targetSelect),
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Local port", localEntry),
		widget.NewFormItem("Bind address", addressEntry),
		widget.NewFormItem("", autostartCheck),
	)

	buttons := container.NewHBox(
		widget.NewButton("Cancel", func() { win.Close() }),
		widget.NewButton("Save", save),
	)

	content := container.NewBorder(
		widget.NewLabelWithStyle("Kubernetes port-forward", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewVBox(status, buttons),
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

// manageRows builds one row widget per configured forward.
func (a *App) manageRows() []fyne.CanvasObject {
	forwards := a.cfg.List()
	if len(forwards) == 0 {
		return []fyne.CanvasObject{widget.NewLabel("No forwards yet. Click “Add Forward…”.")}
	}
	objs := make([]fyne.CanvasObject, 0, len(forwards))
	for _, f := range forwards {
		f := f
		st := a.mgr.Status(f.ID)
		active := a.mgr.Active(f.ID)

		title := widget.NewLabelWithStyle(menuLabel(f, st), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		sub := f.Context + "  ·  " + f.Namespace + "  ·  " + f.TargetKind + "/" + f.TargetName +
			fmt.Sprintf("  ·  %d→%d", f.LocalPort, f.RemotePort)
		if st.State == forward.StateError && st.LastErr != "" {
			sub += "\n⚠ " + st.LastErr
		}
		subLabel := widget.NewLabel(sub)
		subLabel.Wrapping = fyne.TextWrapWord

		toggleText := "Start"
		if active {
			toggleText = "Stop"
		}
		toggleBtn := widget.NewButton(toggleText, func() { a.toggle(f) })
		editBtn := widget.NewButton("Edit", func() {
			cur, _ := a.cfg.Get(f.ID)
			a.showAddWindow(&cur)
		})
		delBtn := widget.NewButton("Delete", func() {
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

		controls := container.NewHBox(toggleBtn, editBtn, delBtn)
		card := container.NewBorder(nil, nil, nil, controls, container.NewVBox(title, subLabel))
		objs = append(objs, card, widget.NewSeparator())
	}
	return objs
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
