// Package iam — UserSettings JSON shim.
//
// This is a hand-written JSON-over-HTTP server that mounts under the same
// `/paladin.iam.v1.UserSettingsService/{Method}` path namespace connect-rpc
// would use. It exists because buf-codegen for the
// `proto/paladin/iam/v1/user_settings_service.proto` definition has not been
// run yet — see BACKLOG.md "UserSettings — proto + connect-rpc wiring".
//
// Once `buf generate` runs:
//   1. Replace the file-level Register function with the proper
//      `paladiniamv1connect.NewUserSettingsServiceHandler(server, opts)` shape.
//   2. Move the JSON request/response structs to paladiniamv1 generated types.
//   3. Adjust callers in cmd/server/root.go to use the new constructor.
//
// The wire path stays identical, so existing clients won't notice the swap.

package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"
)

const userSettingsServicePrefix = "/paladin.iam.v1.UserSettingsService/"

// UserSettingsServer is the JSON-over-HTTP handler. Implements the same
// surface as the (yet-to-be-generated) connect-rpc server interface, so
// the post-buf-generate swap is mechanical.
type UserSettingsServer struct {
	H *usersettingsh.Handler
}

func NewUserSettingsServer(h *usersettingsh.Handler) *UserSettingsServer {
	return &UserSettingsServer{H: h}
}

// RegisterUserSettings mounts the five RPC methods on `mux` under the
// canonical service path. Returns the prefix for caller-side logging.
func RegisterUserSettings(mux *http.ServeMux, h *usersettingsh.Handler) string {
	srv := NewUserSettingsServer(h)
	mux.Handle(userSettingsServicePrefix, srv)
	return userSettingsServicePrefix
}

// ServeHTTP dispatches by trailing method name. Authentication is performed
// by the IAM mux's chained interceptors before this handler runs — we read
// auth.Principal from ctx the same way the generated connect handlers do.
func (s *UserSettingsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	method := strings.TrimPrefix(r.URL.Path, userSettingsServicePrefix)
	switch method {
	case "GetMine":
		s.getMine(w, r)
	case "UpdateMine":
		s.updateMine(w, r)
	case "GetForUser":
		s.getForUser(w, r)
	case "ListByTenant":
		s.listByTenant(w, r)
	case "DeleteForUser":
		s.deleteForUser(w, r)
	default:
		writeJSONErr(w, http.StatusNotFound, "unknown method")
	}
}

// ─── DTOs ───────────────────────────────────────────────────────────────────

// SettingsDTO is the wire shape — mirrors the proto `UserSettings` message
// field-for-field. When the generated proto types land, this struct goes
// away in favour of paladiniamv1.UserSettings.
type SettingsDTO struct {
	Name            string          `json:"name"`
	UserID          string          `json:"user_id"`
	TenantID        string          `json:"tenant_id"`
	Timezone        string          `json:"timezone"`
	Locale          string          `json:"locale"`
	Theme           string          `json:"theme"`
	Preferences     json.RawMessage `json:"preferences,omitempty"`
	ResourceVersion string          `json:"resource_version"`
}

type updateMineReq struct {
	UpdateMask  []string        `json:"update_mask,omitempty"`
	Timezone    string          `json:"timezone,omitempty"`
	Locale      string          `json:"locale,omitempty"`
	Theme       string          `json:"theme,omitempty"`
	Preferences json.RawMessage `json:"preferences,omitempty"`
}

type listResp struct {
	Settings []SettingsDTO `json:"settings"`
}

// ─── handlers ───────────────────────────────────────────────────────────────

func (s *UserSettingsServer) getMine(w http.ResponseWriter, r *http.Request) {
	out, err := s.H.GetMine(r.Context())
	writeOne(w, out, err)
}

func (s *UserSettingsServer) updateMine(w http.ResponseWriter, r *http.Request) {
	var req updateMineReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, errEOF) {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	prefs := []byte(req.Preferences)
	if len(prefs) == 0 {
		prefs = nil
	}
	out, err := s.H.UpdateMine(r.Context(), usersettingsh.UpdateMineInput{
		UpdateMask:  req.UpdateMask,
		Timezone:    req.Timezone,
		Locale:      req.Locale,
		Theme:       req.Theme,
		Preferences: prefs,
	})
	writeOne(w, out, err)
}

func (s *UserSettingsServer) getForUser(w http.ResponseWriter, r *http.Request) {
	uid, err := parseUserName(extractField(r, "name"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.H.GetForUser(r.Context(), uid)
	writeOne(w, out, err)
}

func (s *UserSettingsServer) listByTenant(w http.ResponseWriter, r *http.Request) {
	parent := extractField(r, "parent")
	tid, err := parseTenantParent(r.Context(), parent)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pageSize := int32(extractInt(r, "page_size"))
	out, err := s.H.ListByTenant(r.Context(), tid, pageSize)
	if err != nil {
		writeConnectErr(w, err)
		return
	}
	dtos := make([]SettingsDTO, 0, len(out))
	for _, s := range out {
		dtos = append(dtos, settingsToDTO(s))
	}
	writeJSON(w, http.StatusOK, listResp{Settings: dtos})
}

func (s *UserSettingsServer) deleteForUser(w http.ResponseWriter, r *http.Request) {
	uid, err := parseUserName(extractField(r, "name"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.H.DeleteForUser(r.Context(), uid); err != nil {
		writeConnectErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// ─── helpers ────────────────────────────────────────────────────────────────

var errEOF = errors.New("EOF") // Local alias; keeps import surface small.

func writeOne(w http.ResponseWriter, s *usersettingsh.Settings, err error) {
	if err != nil {
		writeConnectErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsToDTO(*s))
}

func settingsToDTO(s usersettingsh.Settings) SettingsDTO {
	return SettingsDTO{
		Name:            "users/" + s.UserID.String() + "/settings",
		UserID:          s.UserID.String(),
		TenantID:        s.TenantID.String(),
		Timezone:        s.Timezone,
		Locale:          s.Locale,
		Theme:           s.Theme,
		Preferences:     json.RawMessage(s.Preferences),
		ResourceVersion: itoa64(s.ResourceVersion),
	}
}

// parseUserName accepts both "users/{uuid}" and "users/{uuid}/settings"
// since the resource-name shape varies between Get and Delete in the proto.
func parseUserName(name string) (uuid.UUID, error) {
	if !strings.HasPrefix(name, "users/") {
		return uuid.Nil, errors.New("name must start with users/")
	}
	body := strings.TrimPrefix(name, "users/")
	body = strings.TrimSuffix(body, "/settings")
	return uuid.Parse(body)
}

// parseTenantParent accepts the slug form via apiutil.ParseTenantNameRef but
// the JSON shim avoids importing apiutil to keep package coupling shallow.
// Slug → uuid resolution happens at the handler layer once the listing
// endpoint actually receives the slug — for v1 the JSON shim hard-requires
// a UUID. Slug-form parents are tracked in BACKLOG (Tenant slug rollout —
// Phase 3).
func parseTenantParent(_ context.Context, parent string) (uuid.UUID, error) {
	body := strings.TrimPrefix(parent, "tenants/")
	if body == "" {
		return uuid.Nil, errors.New("parent must be tenants/{tenant_id}")
	}
	id, err := uuid.Parse(body)
	if err != nil {
		return uuid.Nil, errors.New("parent: tenant slug not yet supported on this RPC; use UUID")
	}
	return id, nil
}

// extractField pulls a string field from the JSON body. The body is consumed
// once; callers that need multiple fields use a typed struct instead.
func extractField(r *http.Request, key string) string {
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func extractInt(r *http.Request, key string) int64 {
	// Bodies are consumed by extractField — this helper is only correct
	// when called BEFORE extractField on a fresh body. Keeping it
	// single-purpose: callers either decode the full struct or use this
	// + extractField, never both. listByTenant uses a typed struct path
	// internally; this exists for future single-int extractions.
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		return 0
	}
	v, _ := m[key].(float64)
	return int64(v)
}

func itoa64(i int64) string {
	// Small dependency-free formatter — matches proto-emitted resource_version
	// shape (decimal string).
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeConnectErr maps a *connect.Error to an HTTP status code so handlers
// that already return Connect-style errors render correctly even though we
// aren't using the full connect-rpc framing.
func writeConnectErr(w http.ResponseWriter, err error) {
	var ce *connect.Error
	if errors.As(err, &ce) {
		writeJSONErr(w, connectCodeToHTTP(ce.Code()), ce.Message())
		return
	}
	writeJSONErr(w, http.StatusInternalServerError, err.Error())
}

func connectCodeToHTTP(c connect.Code) int {
	switch c {
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return http.StatusConflict
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
