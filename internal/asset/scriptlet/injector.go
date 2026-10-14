package scriptlet

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/hostmatch"
	"github.com/irbis-sh/zen-desktop/internal/redacted"
)

var (
	//go:embed bundle.js
	defaultScriptletsBundle []byte
)

type store interface {
	AddPrimaryRule(hostnamePatterns string, body argList) error
	AddExceptionRule(hostnamePatterns string, body argList) error
	Get(hostname string) []argList
}

// Injector injects scriptlets into HTML HTTP responses.
type Injector struct {
	// bundle contains the scriptlets JS bundle.
	bundle []byte
	// store stores and retrieves scriptlets by hostname.
	store store
}

func NewInjectorWithDefaults() (*Injector, error) {
	// [B4 2026-10-08] Exceptions match by argument-list prefix: a nameless
	// exception disables every scriptlet, a name-only exception disables
	// every same-name scriptlet, and a full argument list matches exactly,
	// as it did before.
	store := hostmatch.NewHostMatcherWithExclusionMatcher[argList](matchExceptionArgList)
	return newInjector(defaultScriptletsBundle, store)
}

// matchExceptionArgList reports whether an exception's argument list
// suppresses a primary rule's argument list: the exception's arguments must
// be a leading subset of the primary's, element-wise. The comparison works
// on the JSON-encoded forms, where the exception's full encoding followed by
// a comma marks the boundary of its last argument, so e.g. the exception
// 'set-cookie' cannot suppress the primary 'set-cookie-reload'. An empty
// exception argument list (a nameless exception) suppresses everything.
func matchExceptionArgList(ex, item argList) bool {
	e, p := string(ex), string(item)
	if e == "" {
		return true
	}
	return e == p || strings.HasPrefix(p, e+",")
}

// newInjector creates a new Injector with the embedded scriptlets.
func newInjector(bundleData []byte, store store) (*Injector, error) {
	if bundleData == nil {
		return nil, errors.New("bundleData is nil")
	}
	if store == nil {
		return nil, errors.New("store is nil")
	}

	return &Injector{
		bundle: bundleData,
		store:  store,
	}, nil
}

// GetAsset returns the scriptlet injection asset for the given hostname.
//
// 2026-10-08 (B2): the $jsinject exemption (and the jsinject component of
// $document) clears all scriptlets.
func (inj *Injector) GetAsset(hostname string, ex ...exemption.Exemption) ([]byte, error) {
	if len(ex) > 0 && ex[0].Jsinject {
		return nil, nil
	}
	argLists := inj.store.Get(hostname)
	log.Printf("got %d scriptlets for %q", len(argLists), redacted.Redacted(hostname))
	if len(argLists) == 0 {
		return nil, nil
	}

	var injection bytes.Buffer
	// The wrapping IIFE keeps the bundle's top-level var from becoming a window
	// property that page scripts could probe for.
	injection.WriteString("(()=>{")
	injection.Write(inj.bundle)
	for _, argLst := range argLists {
		if err := argLst.GenerateInjection(&injection); err != nil {
			return nil, fmt.Errorf("generate injection for scriptlet %q: %v", argLst, err)
		}
	}
	injection.WriteString("})();")

	return injection.Bytes(), nil
}
