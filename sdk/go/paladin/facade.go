package paladin

import "errors"

//go:generate go run ../internal/facadegen/cmd facade_gen.go

// ErrNoAudience is returned by New for WithTokens without a plane: the token
// source cannot tell which plane's token to send.
var ErrNoAudience = errors.New("paladin: WithTokens needs Connect, or WithTokenSource with an audience")

// ErrNoEndpoints is returned by Connect when no plane has a URL.
var ErrNoEndpoints = errors.New("paladin: Connect needs at least one endpoint")

// Endpoints are the base URLs of the three planes. A plane left empty is not
// connected, and its field in Paladin is nil.
type Endpoints struct {
	Data  string
	Admin string
	IAM   string
}

// Paladin holds a client for every service of every connected plane.
type Paladin struct {
	Data  *DataPlane
	Admin *AdminPlane
	IAM   *IAMPlane
}

// WithTokens authenticates every call with a token from ts for the plane the
// call goes to. It needs Connect, which knows each plane's audience; for one
// plane's Client use WithTokenSource.
func WithTokens(ts TokenSource) Option {
	return func(cfg *config) { cfg.anyPlaneTokens = ts }
}

// withAudience tells New which plane it is building, for WithTokens.
func withAudience(audience string) Option {
	return func(cfg *config) { cfg.audience = audience }
}

// Connect returns clients for every service of the planes in ep, built with
// opts. Every plane gets the same options; WithTokens sends each the token
// for its own audience.
func Connect(ep Endpoints, opts ...Option) (*Paladin, error) {
	if ep.Data == "" && ep.Admin == "" && ep.IAM == "" {
		return nil, ErrNoEndpoints
	}
	plane := func(url, audience string) (*Client, error) {
		return New(url, append(append([]Option(nil), opts...), withAudience(audience))...)
	}
	p := &Paladin{}
	if ep.Data != "" {
		c, err := plane(ep.Data, AudienceData)
		if err != nil {
			return nil, err
		}
		p.Data = newDataPlane(c)
	}
	if ep.Admin != "" {
		c, err := plane(ep.Admin, AudienceAdmin)
		if err != nil {
			return nil, err
		}
		p.Admin = newAdminPlane(c)
	}
	if ep.IAM != "" {
		c, err := plane(ep.IAM, AudienceIAM)
		if err != nil {
			return nil, err
		}
		p.IAM = newIAMPlane(c)
	}
	return p, nil
}
