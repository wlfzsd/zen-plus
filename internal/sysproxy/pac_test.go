package sysproxy

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/constants"
)

// TestRenderPacLocalEndpointWinsOverExclusions pins two properties of the local
// endpoint's PAC branch: it precedes exclusion matching, so excluding a parent
// domain cannot route the endpoint DIRECT (where nothing answers for it), and
// it carries no DIRECT fallback, because a direct connection to the endpoint
// can only fail. The matching tolerates reformatting of the template: only the
// carve-out's shape and its position relative to the dnsDomainIs matching
// matter, not the exact layout.
func TestRenderPacLocalEndpointWinsOverExclusions(t *testing.T) {
	t.Parallel()

	pac := string(renderPac(1234, []string{"irbis.sh"}, false))

	carveOutRe := regexp.MustCompile(
		fmt.Sprintf(`if \(host == "%s"\)\s*\{\s*return "PROXY 127\.0\.0\.1:1234";\s*\}`,
			regexp.QuoteMeta(constants.LocalEndpointHost)))
	carveOut := carveOutRe.FindStringIndex(pac)
	if carveOut == nil {
		t.Fatalf("PAC lacks the local endpoint carve-out (want a match for %q):\n%s", carveOutRe, pac)
	}
	if block := pac[carveOut[0]:carveOut[1]]; strings.Contains(block, "DIRECT") {
		t.Fatalf("local endpoint carve-out contains a DIRECT fallback:\n%s", block)
	}

	exclusionsIndex := strings.Index(pac, "dnsDomainIs(")
	if exclusionsIndex == -1 {
		t.Fatalf("PAC lacks exclusion matching:\n%s", pac)
	}

	if carveOut[0] > exclusionsIndex {
		t.Fatalf("local endpoint carve-out at %d comes after exclusion matching at %d:\n%s", carveOut[0], exclusionsIndex, pac)
	}
}

// TestRenderPacChainActiveDropsExclusions pins the chaining behaviour: with an
// upstream configured, the EMBEDDED sensitive-host exclusions must not appear
// in the PAC (a DIRECT answer there sends those hosts around Zen, which on
// censored networks makes them unreachable), while user-configured exclusions
// are always honored in every mode - the user's explicit choice outranks the
// chaining default. The embedded entries are read from the same embedded list
// the production code uses, so the test stays generic: whatever the list
// contains, none of it may leak into a chain-active PAC.
func TestRenderPacChainActiveDropsExclusions(t *testing.T) {
	t.Parallel()

	embedded := embeddedExclusionHosts(t)
	if len(embedded) == 0 {
		t.Fatal("embedded exclusion list is empty; the drop-exclusions property is untestable")
	}

	pac := string(renderPac(1234, []string{"opted-out.example"}, true))
	for _, host := range embedded {
		if strings.Contains(pac, host) {
			t.Fatalf("chain-active PAC still carries embedded exclusion %q:\n%s", host, pac)
		}
	}
	if !strings.Contains(pac, "opted-out.example") {
		t.Fatalf("chain-active PAC dropped a user-configured exclusion:\n%s", pac)
	}
	if !strings.Contains(pac, `var excludedHosts = ["opted-out.example"];`) {
		t.Fatalf("chain-active PAC exclusion list is not exactly the user list:\n%s", pac)
	}

	pac = string(renderPac(1234, []string{"irbis.sh"}, false))
	if !strings.Contains(pac, "irbis.sh") {
		t.Fatalf("non-chain PAC lost the exclusion list:\n%s", pac)
	}
	for _, host := range embedded {
		if !strings.Contains(pac, host) {
			t.Fatalf("non-chain PAC lost embedded exclusion %q:\n%s", host, pac)
		}
	}
}

// embeddedExclusionHosts parses the embedded common exclusion list the same
// way buildExcludedHosts does.
func embeddedExclusionHosts(t *testing.T) []string {
	t.Helper()

	var hosts []string
	for _, line := range strings.Split(string(commonExcludedHosts), "\n") {
		if hashIndex := strings.IndexByte(line, '#'); hashIndex != -1 {
			line = line[:hashIndex]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			hosts = append(hosts, line)
		}
	}
	return hosts
}
