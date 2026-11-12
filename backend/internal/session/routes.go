package session

import (
	"strings"

	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Plane is a backend plane the role forwards to.
type Plane string

// The planes.
const (
	PlaneData  Plane = "data"
	PlaneAdmin Plane = "admin"
	PlaneIAM   Plane = "iam"
)

// planePackages are the proto package each plane serves, as a procedure
// path begins with it: /<package>.<Service>/<Method>.
var planePackages = map[string]Plane{
	"paladin.data.v1.":  PlaneData,
	"paladin.admin.v1.": PlaneAdmin,
	"paladin.iam.v1.":   PlaneIAM,
}

// planeAudience is the audience of the access token a plane accepts.
var planeAudience = map[Plane]string{
	PlaneData:  paladin.AudienceData,
	PlaneAdmin: paladin.AudienceAdmin,
	PlaneIAM:   paladin.AudienceIAM,
}

// tokenProcedures hand out or consume the session's tokens. The browser
// reaches them only through the role's own endpoints, which keep the tokens
// out of its reach; forwarded, they would hand it one.
var tokenProcedures = map[string]bool{
	paladiniamv1connect.AuthServiceLoginProcedure:            true,
	paladiniamv1connect.AuthServiceRefreshTokenProcedure:     true,
	paladiniamv1connect.AuthServiceExchangeAudienceProcedure: true,
	paladiniamv1connect.AuthServiceSwitchTenantProcedure:     true,
	paladiniamv1connect.AuthServiceRevokeProcedure:           true,
}

// planeOf is the plane that serves procedure, "/<package>.<Service>/<Method>".
func planeOf(procedure string) (Plane, bool) {
	name := strings.TrimPrefix(procedure, "/")
	service, method, ok := strings.Cut(name, "/")
	if !ok || name == procedure || service == "" || method == "" || strings.Contains(method, "/") {
		return "", false
	}
	for pkg, plane := range planePackages {
		if strings.HasPrefix(service, pkg) && len(service) > len(pkg) {
			return plane, true
		}
	}
	return "", false
}
