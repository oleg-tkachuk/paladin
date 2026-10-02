package paladin

import "runtime/debug"

// modulePath is this SDK's module, as a dependency of the program using it.
const modulePath = "github.com/oleg-tkachuk/paladin/sdk/go"

// userAgentProduct names the SDK in HeaderUserAgent.
const userAgentProduct = "paladin-sdk-go"

// develVersion is reported when the SDK is not a versioned dependency: a
// build inside this repository, or a replace directive.
const develVersion = "devel"

// userAgent is "paladin-sdk-go/<version>", the version being the module's as
// the program was built with it — the same number as the API contract.
func userAgent() string {
	return userAgentProduct + "/" + moduleVersion(debug.ReadBuildInfo)
}

func moduleVersion(read func() (*debug.BuildInfo, bool)) string {
	info, ok := read()
	if !ok {
		return develVersion
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath && dep.Replace == nil && dep.Version != "" {
			return dep.Version
		}
	}
	return develVersion
}
