// Package kube provides read-only discovery of cluster objects by shelling out
// to the user's kubectl binary. Going through kubectl means we inherit whatever
// auth (e.g. EKS exec credential plugins) already works on the command line,
// and we avoid pulling the large client-go dependency tree into the app.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dawidlaszuk/k8s-tray-forwarder/internal/config"
)

// defaultTimeout bounds discovery calls so a wedged context can't hang the UI.
const defaultTimeout = 20 * time.Second

// Resource is a discovered forward target plus any ports we could infer, used
// to prefill the remote-port field in the UI.
type Resource struct {
	Name  string
	Ports []int
}

// run executes kubectl with the given args and returns stdout, surfacing
// stderr in the error so auth/permission problems are visible to the user.
func run(ctx context.Context, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(c, Binary(), args...)
	cmd.Env = Env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Contexts lists kubeconfig context names.
func Contexts(ctx context.Context) ([]string, error) {
	out, err := run(ctx, "config", "get-contexts", "-o", "name")
	if err != nil {
		return nil, err
	}
	return sortedLines(out), nil
}

// Namespaces lists namespaces in the given context.
func Namespaces(ctx context.Context, kubeContext string) ([]string, error) {
	out, err := run(ctx, "--context", kubeContext, "get", "namespaces", "-o", "name")
	if err != nil {
		return nil, err
	}
	// kubectl prints "namespace/<name>"; strip the resource prefix.
	var names []string
	for _, l := range sortedLines(out) {
		names = append(names, strings.TrimPrefix(l, "namespace/"))
	}
	return names, nil
}

// itemList is the minimal shape we need from `kubectl get <kind> -o json`.
type itemList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			// Services expose ports here.
			Ports []struct {
				Port int `json:"port"`
			} `json:"ports"`
			// Pods expose containers directly...
			Containers []container `json:"containers"`
			// ...Deployments (and other workloads) nest them under a template.
			Template struct {
				Spec struct {
					Containers []container `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	} `json:"items"`
}

type container struct {
	Ports []struct {
		ContainerPort int `json:"containerPort"`
	} `json:"ports"`
}

// Resources lists forward targets of the given kind (deployment/service/pod)
// in a namespace, including inferred ports for port prefilling.
func Resources(ctx context.Context, kubeContext, namespace, kind string) ([]Resource, error) {
	k, err := kubectlKind(kind)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, "--context", kubeContext, "-n", namespace, "get", k, "-o", "json")
	if err != nil {
		return nil, err
	}
	var list itemList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("decode %s json: %w", k, err)
	}
	resources := make([]Resource, 0, len(list.Items))
	for _, it := range list.Items {
		r := Resource{Name: it.Metadata.Name}
		seen := map[int]bool{}
		add := func(p int) {
			if p > 0 && !seen[p] {
				seen[p] = true
				r.Ports = append(r.Ports, p)
			}
		}
		for _, p := range it.Spec.Ports { // services
			add(p.Port)
		}
		for _, c := range it.Spec.Containers { // pods
			for _, p := range c.Ports {
				add(p.ContainerPort)
			}
		}
		for _, c := range it.Spec.Template.Spec.Containers { // deployments
			for _, p := range c.Ports {
				add(p.ContainerPort)
			}
		}
		sort.Ints(r.Ports)
		resources = append(resources, r)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
	return resources, nil
}

func kubectlKind(kind string) (string, error) {
	switch kind {
	case config.KindDeployment:
		return "deployments", nil
	case config.KindService:
		return "services", nil
	case config.KindPod:
		return "pods", nil
	default:
		return "", fmt.Errorf("unknown kind %q", kind)
	}
}

func sortedLines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}
