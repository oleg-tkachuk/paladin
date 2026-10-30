package oauth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// The lines logRequests writes, one per request an endpoint answers.
const (
	requestServedMessage  = "oauth request served"
	requestRefusedMessage = "oauth request refused"
)

// The fields of those lines. None carries a secret: a token, a code, a
// password or a verifier never leaves the request.
const (
	fieldEndpoint  = "endpoint"
	fieldClientID  = "client_id"
	fieldGrantType = "grant_type"
	fieldStatus    = "status"
	fieldError     = "error"
	fieldTookMS    = "took_ms"
)

// The form members logRequests reads after the endpoint parsed the form, and
// the RFC 6749 error member a refusal names its code in — a query parameter
// of a redirect (§4.1.2.1) or a member of a JSON body (§5.2).
const (
	formClientID  = "client_id"
	formGrantType = "grant_type"
	errorMember   = "error"
)

// errorBodyLimit bounds how much of a refusal's body logRequests keeps to
// read its error code: an RFC 6749 error body is a few dozen bytes.
const errorBodyLimit = 4096

// firstServerErrorStatus is the lowest status that reports the server
// failing. Those are logged with their cause where they happen, and by the
// listener's middleware, so logRequests leaves them.
const firstServerErrorStatus = http.StatusInternalServerError

// firstClientErrorStatus is the lowest status that refuses the request.
const firstClientErrorStatus = http.StatusBadRequest

// logRequests serves endpoint through next and logs the outcome: a refusal
// — a 4xx, or a redirect carrying an error to the client — at warn with its
// error code, anything else short of a server error at info. A request
// handed on with loggedDownstream is the other handler's to log.
func logRequests(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &outcomeRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next(rec, r)
		if rec.loggedDownstream || rec.status >= firstServerErrorStatus {
			return
		}
		errCode := rec.errorCode()
		fields := []zap.Field{
			zap.String(fieldEndpoint, endpoint),
			zap.String(fieldClientID, clientIDOf(r)),
			zap.Int(fieldStatus, rec.status),
			zap.Int64(fieldTookMS, time.Since(start).Milliseconds()),
		}
		if grant := r.PostForm.Get(formGrantType); grant != "" {
			fields = append(fields, zap.String(fieldGrantType, grant))
		}
		log := logger.FromContext(r.Context())
		if rec.status >= firstClientErrorStatus || errCode != "" {
			log.Warn(requestRefusedMessage, append(fields, zap.String(fieldError, errCode))...)
			return
		}
		log.Info(requestServedMessage, fields...)
	}
}

// clientIDOf is the client the request names, in its form or its Basic
// credentials, as the endpoints read it.
func clientIDOf(r *http.Request) string {
	if id := r.Form.Get(formClientID); id != "" {
		return id
	}
	if id, _, ok := r.BasicAuth(); ok {
		return id
	}
	return r.URL.Query().Get(formClientID)
}

// loggedDownstream marks the request w answers as handed to a handler that
// logs its outcome itself, so logRequests leaves it.
func loggedDownstream(w http.ResponseWriter) {
	if rec, ok := w.(*outcomeRecorder); ok {
		rec.loggedDownstream = true
	}
}

// outcomeRecorder remembers the status an endpoint answered with, and enough
// of a refusal's body to read its error code. It unwraps to the writer it
// wraps.
type outcomeRecorder struct {
	http.ResponseWriter
	status           int
	wroteHeader      bool
	loggedDownstream bool
	body             bytes.Buffer
}

func (o *outcomeRecorder) WriteHeader(code int) {
	if !o.wroteHeader {
		o.status, o.wroteHeader = code, true
	}
	o.ResponseWriter.WriteHeader(code)
}

func (o *outcomeRecorder) Write(b []byte) (int, error) {
	o.wroteHeader = true
	if o.status >= firstClientErrorStatus && o.body.Len() < errorBodyLimit {
		o.body.Write(b[:min(len(b), errorBodyLimit-o.body.Len())])
	}
	return o.ResponseWriter.Write(b)
}

func (o *outcomeRecorder) Unwrap() http.ResponseWriter { return o.ResponseWriter }

// errorCode is the RFC 6749 error the endpoint answered with: from the
// redirect it sent the client, or from its JSON body; empty for none.
func (o *outcomeRecorder) errorCode() string {
	if loc := o.Header().Get("Location"); loc != "" {
		if u, err := url.Parse(loc); err == nil {
			if code := u.Query().Get(errorMember); code != "" {
				return code
			}
		}
	}
	if o.body.Len() == 0 {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal(o.body.Bytes(), &body); err != nil {
		return ""
	}
	code, _ := body[errorMember].(string)
	return code
}
