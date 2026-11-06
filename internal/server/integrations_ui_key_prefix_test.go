package server

import (
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/integration"
)

// The access key page checks the key it just received before showing it. A
// prefix change on the server that the page does not know about makes the page
// report an incomplete response although the key was created (ticket 22 followed
// by ticket 23 found this in the browser, not in an API test).
func TestIntegrationsPageAcceptsTheKeyPrefixTheServerIssues(t *testing.T) {
	raw, err := os.ReadFile("../../web/admin/assets/integrations.js")
	if err != nil {
		t.Fatalf("read integrations.js: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, `startsWith("`+integration.KeyPrefix+`")`) {
		t.Errorf("integrations.js does not accept keys that start with %q, the prefix the server issues", integration.KeyPrefix)
	}
	if strings.Contains(script, `Bearer jla_`) {
		t.Errorf("integrations.js still shows %q in its connection text; new keys use %q", "Bearer jla_", integration.KeyPrefix)
	}
}
