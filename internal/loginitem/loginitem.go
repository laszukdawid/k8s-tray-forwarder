// Package loginitem manages a macOS LaunchAgent so the app can optionally start
// when the user logs in. Rather than bridging into the Objective-C SMAppService
// API, it writes a plist into ~/Library/LaunchAgents — launchd loads agents
// there at login automatically, so no privileged calls or subprocesses are
// needed. Toggling is just creating or removing that file.
package loginitem

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Label is the LaunchAgent's reverse-DNS identifier and plist filename stem.
const Label = "com.github.dawidlaszuk.k8s-tray-forwarder"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// Enabled reports whether the LaunchAgent is currently installed.
func Enabled() bool {
	p, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Sync reconciles the on-disk LaunchAgent with the desired state. Changes take
// effect at the next login (we deliberately do not bootstrap into the current
// session, which would spawn a duplicate instance).
func Sync(want bool) error {
	if runtime.GOOS != "darwin" {
		if want {
			return fmt.Errorf("launch at login is only supported on macOS")
		}
		return nil
	}
	if want {
		return enable()
	}
	return disable()
}

func enable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	args, err := programArguments()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(renderPlist(args)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// programArguments returns the launchd ProgramArguments for the running app.
// When launched from a .app bundle we go through `open` so LaunchServices
// activates the bundle properly (icon, single-instance); otherwise we point
// launchd straight at the executable (the dev/raw-binary case).
func programArguments() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if bundle, ok := appBundle(exe); ok {
		return []string{"/usr/bin/open", bundle}, nil
	}
	return []string{exe}, nil
}

// appBundle returns the enclosing .app path if exe lives inside a macOS bundle.
func appBundle(exe string) (string, bool) {
	const marker = ".app/Contents/MacOS/"
	if i := strings.Index(exe, marker); i != -1 {
		return exe[:i+len(".app")], true
	}
	return "", false
}

func renderPlist(args []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + xmlEscape(Label) + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range args {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	// Interactive: this is a user-facing GUI app, not a background daemon.
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
