# k8s-tray-forwarder

A macOS menu-bar (system tray) app for toggling Kubernetes port-forwards on and
off. Each configured forward shows up as a checkable item in the tray; flip it
on to run `kubectl port-forward` in the background, flip it off to stop. New
forwards are added through a window that suggests the deployments / services /
pods discovered in the context + namespace you pick.

Built with Go + [Fyne](https://fyne.io). Port-forwards and resource discovery
go through your local `kubectl`, so whatever auth already works on the command
line (e.g. EKS `aws eks get-token`) works here too — no separate credentials.

## Features

- **Tray toggles** — one checkable item per forward (`●` running, `○` stopped,
  `⟳` starting/reconnecting, `⚠` error/retrying).
- **Auto-reconnect** — if a forward drops (pod rescheduled, token refresh,
  network blip) it is relaunched with exponential backoff until you stop it.
- **Add/Edit window** — cascading **context → namespace → kind → target**
  dropdowns. Targets are discovered live and the remote port is prefilled from
  the resource's declared ports.
- **Manage window** — list every forward with start/stop/edit/delete plus a live
  activity log.
- **Config file + UI** — everything is stored in a YAML file you can hand-edit;
  the UI reads and writes the same file. `Reload Config` picks up manual edits.
- **Per-forward autostart** — mark a forward `autoStart: true` to bring it up on
  launch.
- **Launch at login** — set `launchAtLogin: true` (or use the Manage-window
  checkbox) to install a macOS LaunchAgent that starts the app when you log in.

## Requirements

- macOS, Go 1.26+, a C toolchain (Xcode command line tools — `xcode-select --install`).
- `kubectl` on your `PATH`, with a working kubeconfig.
- [Task](https://taskfile.dev) (`brew install go-task`) to use the `task` commands below.

## Run

```sh
task run            # go run .
# or
task build && ./k8s-tray-forwarder
```

The app has no main window — look for the icon in the menu bar. Run `task` with
no arguments to list all available tasks.

## Package as a macOS .app

```sh
task install-fyne   # one-time: installs the fyne packaging CLI
task bundle         # produces "K8s Port Forwards.app"
```

Drag the `.app` into `/Applications`, then enable **Launch at login** from the
Manage window (or set `launchAtLogin: true` in the config). For launch-at-login
to point at a stable path, enable it from the installed `.app` rather than from
`task run` — the LaunchAgent records whatever executable launched the app.

> `task bundle` expects an `icon.png` in the project root. Drop any square PNG
> there (1024×1024 recommended) before bundling.

## Configuration

Config lives at:

```
~/Library/Application Support/k8s-tray-forwarder/config.yaml
```

Override the location with `K8S_TRAY_FORWARDER_CONFIG=/path/to/config.yaml`.
See [`config.example.yaml`](./config.example.yaml) for the full field list. A
minimal entry:

```yaml
launchAtLogin: false       # start the app at login (macOS LaunchAgent)

forwards:
  - name: Postgres (project)
    type: kubernetes
    context: personal-k8s
    namespace: project
    targetKind: service      # deployment | service | pod
    targetName: postgresql
    remotePort: 5432
    localPort: 5432          # optional; defaults to remotePort
    autoStart: true
```

With that, the internal database is reachable at `127.0.0.1:5432` whenever the
toggle is on — run queries with `psql -h 127.0.0.1 -p 5432` or point any DB
client at localhost without exposing it outside the cluster.

## Project layout

```
main.go                      entry point
internal/config              YAML config: load/save/validate
internal/kube                read-only discovery via kubectl (contexts/ns/resources)
internal/forward             supervised kubectl port-forward processes + state machine
internal/ui                  Fyne tray menu, Add/Edit window, Manage window
```

## Notes / future ideas

- The `type` field is `kubernetes` today; the schema leaves room for other
  connection types (SSH tunnels, raw TCP) later.
- Discovery and port-forwarding shell out to `kubectl`; swapping in `client-go`
  would make the binary fully self-contained at the cost of a much larger
  dependency tree and re-implementing exec-auth.
```
