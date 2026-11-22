package apiutil_test

import (
	"regexp"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/capabilityh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"

	// Links every handler and store package, so each one's init has
	// registered its sentinels before the test reads the registry.
	_ "github.com/oleg-tkachuk/paladin/backend/internal/app"
)

// packagePrefix is how a Go package names itself at the front of an error,
// "auth/store: ...": the implementation's name, not anything a caller can act
// on.
var packagePrefix = regexp.MustCompile(`^[a-z_/]+: `)

// minRegistered is fewer sentinels than the server registers: a registry this
// small means the packages were not linked and the test checked nothing.
const minRegistered = 40

// MapError sends a sentinel's text as the refusal's message, so every
// registered sentinel speaks of the resource and the reason, not of the
// package that defines it.
func TestRegisteredSentinelsNameNoPackage(t *testing.T) {
	sentinels := apiutil.RegisteredSentinels()
	if len(sentinels) < minRegistered {
		t.Fatalf("%d sentinels registered, want at least %d: the handler packages are not linked", len(sentinels), minRegistered)
	}
	for _, err := range sentinels {
		if packagePrefix.MatchString(err.Error()) {
			t.Errorf("%q starts with a package name", err)
		}
	}
}

// A refused bearer token is answered by the auth interceptors, not MapError,
// so its messages are not in the registry: held to the same rule here.
func TestBearerRefusalsNameNoPackage(t *testing.T) {
	for _, msg := range auth.BearerRefusals() {
		if packagePrefix.MatchString(msg) {
			t.Errorf("%q starts with a package name", msg)
		}
	}
}

// A lease or Delegate call refused for the capability it presents is answered
// by the capability handler's own messages, not MapError: held to the same
// rule here.
func TestPresentedCapabilityRefusalsNameNoPackage(t *testing.T) {
	for _, msg := range capabilityh.PresentedRefusals() {
		if packagePrefix.MatchString(msg) {
			t.Errorf("%q starts with a package name", msg)
		}
	}
}
