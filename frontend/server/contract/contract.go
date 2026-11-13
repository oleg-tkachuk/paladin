// Package contract holds the names the console's pages and its server must
// spell alike: the session cookie, the paths the server answers itself, the
// paths open without a session, and the placeholder the static export builds
// each dynamic segment under (ADR-0049).
//
// contract.json is the one source. The pages import it as JSON; this package
// embeds it, so neither side carries its own copy.
package contract

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed contract.json
var raw []byte

// Names are the shared names.
type Names struct {
	// SessionCookie is the session role's cookie; its presence alone decides
	// whether a page is served or the browser is sent to sign in.
	SessionCookie string `json:"sessionCookie"`
	// LoginPath is where a request without a session is sent.
	LoginPath string `json:"loginPath"`
	// NextParam carries the path the login page returns to.
	NextParam string `json:"nextParam"`
	// BannerPath serves the operator's banner.
	BannerPath string `json:"bannerPath"`
	// LivePath is the kubelet probe.
	LivePath string `json:"livePath"`
	// ConsentPath is the OAuth consent page, whose form may end anywhere.
	ConsentPath string `json:"consentPath"`
	// AuthorizePath receives the consent form and is relayed to the IAM plane.
	AuthorizePath string `json:"authorizePath"`
	// HealthPath is the health page, where /ops now leads.
	HealthPath string `json:"healthPath"`
	// StorageBackendsPath lists the backends; a backend's page and its
	// buckets' pages are under it.
	StorageBackendsPath string `json:"storageBackendsPath"`
	// PublicPrefixes are served without a session cookie.
	PublicPrefixes []string `json:"publicPrefixes"`
	// PlaceholderSegment is the value each dynamic segment is exported under.
	PlaceholderSegment string `json:"placeholderSegment"`
}

// Load parses the embedded contract, failing on any name left empty.
func Load() (Names, error) {
	var n Names
	if err := json.Unmarshal(raw, &n); err != nil {
		return Names{}, fmt.Errorf("parse contract.json: %w", err)
	}
	for field, v := range map[string]string{
		"sessionCookie":       n.SessionCookie,
		"loginPath":           n.LoginPath,
		"nextParam":           n.NextParam,
		"bannerPath":          n.BannerPath,
		"livePath":            n.LivePath,
		"consentPath":         n.ConsentPath,
		"authorizePath":       n.AuthorizePath,
		"healthPath":          n.HealthPath,
		"storageBackendsPath": n.StorageBackendsPath,
		"placeholderSegment":  n.PlaceholderSegment,
	} {
		if v == "" {
			return Names{}, fmt.Errorf("contract.json: %s is empty", field)
		}
	}
	if len(n.PublicPrefixes) == 0 {
		return Names{}, fmt.Errorf("contract.json: publicPrefixes is empty")
	}
	return n, nil
}
