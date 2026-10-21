package paladin

// Plane is a plane by the label a capability's audience and an Attenuation
// name it. A label is not the plane's bearer audience: a capability issued
// for AudienceData rather than PlaneData is refused as an audience mismatch.
// Audience maps one to the other.
type Plane string

// The planes, as a capability names them. The server imports these, and
// sdk/testdata/planes.json holds both SDKs to the same labels.
const (
	PlaneData  Plane = "data"
	PlaneAdmin Plane = "admin"
	PlaneIAM   Plane = "iam"
	PlaneMCP   Plane = "mcp"
)

// Audience is the bearer audience of the same plane, what a session's token
// for it carries; "" for a label that is none of these.
func (p Plane) Audience() string {
	switch p {
	case PlaneData:
		return AudienceData
	case PlaneAdmin:
		return AudienceAdmin
	case PlaneIAM:
		return AudienceIAM
	case PlaneMCP:
		return AudienceMCP
	}
	return ""
}

// PlaneNames is planes as a capability's audience spells them:
//
//	req := &adminv1.CapabilityServiceIssueRequest{Audience: paladin.PlaneNames(paladin.PlaneData)}
func PlaneNames(planes ...Plane) []string {
	names := make([]string, len(planes))
	for i, p := range planes {
		names[i] = string(p)
	}
	return names
}
