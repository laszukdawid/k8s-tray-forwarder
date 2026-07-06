package config

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	c, err := Load(path) // missing file -> empty config
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(c.List()) != 0 {
		t.Fatalf("expected no forwards, got %d", len(c.List()))
	}

	saved, err := c.Upsert(Forward{
		Name: "Postgres", Type: TypeKubernetes, Context: "personal-k8s",
		Namespace: "project", TargetKind: KindService, TargetName: "postgresql",
		RemotePort: 5432, LocalPort: 5432, AutoStart: true,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated ID")
	}

	// Reload from disk and confirm persistence.
	c2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := c2.Get(saved.ID)
	if !ok {
		t.Fatalf("forward %s not persisted", saved.ID)
	}
	if got.Name != "Postgres" || got.RemotePort != 5432 || !got.AutoStart {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Update in place (same ID) must replace, not append.
	got.LocalPort = 4100
	if _, err := c2.Upsert(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if n := len(c2.List()); n != 1 {
		t.Fatalf("expected 1 forward after update, got %d", n)
	}

	if err := c2.Delete(saved.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := len(c2.List()); n != 0 {
		t.Fatalf("expected 0 forwards after delete, got %d", n)
	}
}

func TestNormalizeDedupesIDs(t *testing.T) {
	c := &Config{Forwards: []Forward{
		{ID: "a", Name: "one"},
		{ID: "a", Name: "two"},  // duplicate -> must be reassigned
		{ID: "", Name: "three"}, // missing -> must be assigned
		{ID: "b", Name: "four"}, // unique -> preserved
	}}
	c.normalize()

	seen := map[string]bool{}
	for _, f := range c.Forwards {
		if f.ID == "" {
			t.Fatalf("forward %q left without an ID", f.Name)
		}
		if seen[f.ID] {
			t.Fatalf("duplicate ID %q after normalize", f.ID)
		}
		seen[f.ID] = true
	}
	if c.Forwards[0].ID != "a" {
		t.Errorf("first unique ID should be preserved, got %q", c.Forwards[0].ID)
	}
	if c.Forwards[3].ID != "b" {
		t.Errorf("unique ID b should be preserved, got %q", c.Forwards[3].ID)
	}
}

func TestGroupForwards(t *testing.T) {
	forwards := []Forward{
		{ID: "a", Name: "a", Group: "db"},
		{ID: "b", Name: "b"},                // ungrouped -> standalone
		{ID: "c", Name: "c", Group: "db"},   // joins the earlier "db" group
		{ID: "d", Name: "d", Group: " db "}, // trimmed -> same group
		{ID: "e", Name: "e", Group: "web"},
	}

	groups := GroupForwards(forwards)
	if len(groups) != 3 {
		t.Fatalf("expected 3 display groups, got %d: %+v", len(groups), groups)
	}

	// Order follows first appearance: db, (ungrouped b), web.
	if groups[0].Name != "db" || len(groups[0].Forwards) != 3 {
		t.Fatalf("first group should be db with 3 forwards, got %q/%d", groups[0].Name, len(groups[0].Forwards))
	}
	if groups[1].Name != "" || len(groups[1].Forwards) != 1 || groups[1].Forwards[0].ID != "b" {
		t.Fatalf("second entry should be ungrouped singleton b, got %+v", groups[1])
	}
	if groups[2].Name != "web" || len(groups[2].Forwards) != 1 {
		t.Fatalf("third group should be web with 1 forward, got %q/%d", groups[2].Name, len(groups[2].Forwards))
	}

	// Membership within the db group preserves input order.
	got := []string{groups[0].Forwards[0].ID, groups[0].Forwards[1].ID, groups[0].Forwards[2].ID}
	want := []string{"a", "c", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("db group order = %v, want %v", got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	base := Forward{
		Name: "x", Type: TypeKubernetes, Context: "c", Namespace: "n",
		TargetKind: KindService, TargetName: "svc", RemotePort: 80, LocalPort: 8080,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid forward rejected: %v", err)
	}
	if base.BindAddress() != "127.0.0.1" {
		t.Fatalf("default bind address wrong: %q", base.BindAddress())
	}

	bad := base
	bad.TargetKind = "configmap"
	if bad.Validate() == nil {
		t.Fatal("expected invalid targetKind to be rejected")
	}

	bad = base
	bad.RemotePort = 0
	if bad.Validate() == nil {
		t.Fatal("expected zero remote port to be rejected")
	}
}
