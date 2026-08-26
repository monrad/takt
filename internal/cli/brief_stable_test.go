//nolint:testpackage // tests an unexported helper
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReuseBriefTokenFallsBackWhenTheRenderRefusesTheOldToken pins the one
// fallback that is not a missing file: the brief on disk carries a token,
// but the content has since grown that very token — a rejected agent reply
// quoted back on a retry is the way it happens — so [brief.Quote] refuses to
// delimit with it and the re-render fails. Reusing it anyway would hand the
// agent a brief whose END marker sits in the middle of the data, so the
// helper reports no reuse and writeStableBrief writes its fresh-token render
// instead (spec §5.4).
func TestReuseBriefTokenFallsBackWhenTheRenderRefusesTheOldToken(t *testing.T) {
	t.Parallel()
	const tok = "UNTRUSTED-ARTIFACT-00112233445566aa"
	p := filepath.Join(t.TempDir(), "planner.a1.md")
	body := "BEGIN " + tok + " spec.md\nthe agent echoed " + tok + " back\nEND " + tok + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := ""
	refuse := func(want string) (string, string, error) {
		asked = want
		return "", "", errors.New("brief: delimiter token collides with the content; regenerate the token")
	}
	text, unchanged := reuseBriefToken(p, refuse)
	if text != "" || unchanged {
		t.Fatalf(`reuseBriefToken = (%q, %v), want ("", false)`, text, unchanged)
	}
	if asked != tok {
		t.Fatalf("the re-render must be asked for the token on disk: %q != %q", asked, tok)
	}
}
