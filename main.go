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

func main() {
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

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "k8s-tray-forwarder: "+format+"\n", args...)
	os.Exit(1)
}
