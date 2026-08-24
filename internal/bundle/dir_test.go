package bundle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/bundle"
)

func TestResolveDirPrecedence(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	home := t.TempDir()
	cases := []struct {
		name, flag, env, cfg, wantBase string
		wantInRepo                     bool
	}{
		{"default", "", "", "", filepath.Join(repo, "docs", "takt"), true},
		{"cfg", "", "", "plans", filepath.Join(repo, "plans"), true},
		{"env beats cfg", "", "/var/takt", "plans", "/var/takt", false},
		{"flag beats env", "x", "/var/takt", "plans", filepath.Join(repo, "x"), true},
		{"tilde", "", "~/runs", "", filepath.Join(home, "runs"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := bundle.ResolveDir(repo, home, c.flag, c.env, c.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if d.Base != c.wantBase || d.InRepo != c.wantInRepo {
				t.Fatalf("got %+v, want base=%q inRepo=%v", d, c.wantBase, c.wantInRepo)
			}
		})
	}
}

func TestResolveDirRejectsEscapingRelative(t *testing.T) {
	t.Parallel()
	if _, err := bundle.ResolveDir(t.TempDir(), t.TempDir(), "../elsewhere", "", ""); err == nil {
		t.Fatal("relative dir outside the repo must be rejected")
	}
}

func TestResolveDirAbsoluteInsideRepoIsStillExternal(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	abs := filepath.Join(repo, "docs", "takt")
	d, err := bundle.ResolveDir(repo, t.TempDir(), "", abs, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Base != abs || d.InRepo {
		t.Fatalf(
			"got %+v, want base=%q inRepo=false (absolute values are always external, spec §4.1, regardless of where they resolve)",
			d,
			abs,
		)
	}
	want := filepath.Join(abs, filepath.Base(repo), "demo")
	if got := d.Bundle("demo"); got != want {
		t.Fatalf("Bundle(demo) = %q, want %q (repo-name namespacing applies because it is external)", got, want)
	}
}

func TestBundlePathInRepoAndExternal(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	in, _ := bundle.ResolveDir(repo, t.TempDir(), "", "", "")
	if got := in.Bundle("demo"); got != filepath.Join(repo, "docs", "takt", "demo") {
		t.Fatalf("in-repo bundle = %q", got)
	}
	ext, _ := bundle.ResolveDir(repo, t.TempDir(), "", "/srv/takt", "")
	if got := ext.Bundle("demo"); got != filepath.Join("/srv/takt", "myrepo", "demo") {
		t.Fatalf("external bundle = %q (external dirs are namespaced by repo name)", got)
	}
}

func TestListSlugs(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	d, _ := bundle.ResolveDir(repo, t.TempDir(), "", "", "")
	for _, s := range []string{"b", "a"} {
		if err := os.MkdirAll(d.Bundle(s), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.Bundle(s), "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(d.Bundle("no-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListSlugs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ListSlugs = %v (sorted, state.json required)", got)
	}
}

func TestRelToRepo(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	d, _ := bundle.ResolveDir(repo, t.TempDir(), "", "", "")
	rel, err := d.RelToRepo(filepath.Join(repo, "docs", "takt", "x", "spec.md"))
	if err != nil || rel != "docs/takt/x/spec.md" {
		t.Fatalf("RelToRepo = %q, %v", rel, err)
	}
	if _, err = d.RelToRepo("/elsewhere/spec.md"); err == nil {
		t.Fatal("paths outside the repo must error")
	}
}
