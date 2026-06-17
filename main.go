// Command k8s-tray-forwarder is a macOS menu-bar app for toggling Kubernetes
// port-forwards. Configured forwards appear as checkable items in the tray;
// new ones are added through a window that suggests deployments/services/pods
// discovered in the selected context and namespace.
package main

import (
	"fmt"
	"os"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-v", "--version":
			fmt.Printf("k8s-tray-forwarder %s\n", version)
			return
		case "-h", "--help":
			usage()
			return
		default:
			fmt.Fprintf(os.Stderr, "k8s-tray-forwarder: unknown argument %q (try --help)\n", arg)
			os.Exit(2)
		}
	}

	path, err := config.DefaultPath()
	if err != nil {
		fatal("resolve config path: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fatal("load config: %v", err)
	}

	app, err := ui.NewApp(cfg)
	if err != nil {
		fatal("%v", err)
	}
	app.Run()
}

func usage() {
	fmt.Printf(`k8s-tray-forwarder %s — macOS menu-bar app for toggling Kubernetes port-forwards.

Usage:
  k8s-tray-forwarder            Launch the menu-bar app.
  k8s-tray-forwarder --version  Print the version and exit.
  k8s-tray-forwarder --help     Show this help and exit.

Config:
  ~/Library/Application Support/k8s-tray-forwarder/config.yaml
  Override with K8S_TRAY_FORWARDER_CONFIG=/path/to/config.yaml
`, version)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "k8s-tray-forwarder: "+format+"\n", args...)
	os.Exit(1)
}
