package auth

import (
	"errors"
	"net/http"

	"go.uber.org/zap"
)

// msgKeySetUnavailableLog is the line an HTTP edge leaves when it could not
// check a bearer token because the key set would not load.
const msgKeySetUnavailableLog = "bearer token not checked: the key set could not be loaded"

// AnswerKeySetUnavailable answers, for an HTTP edge that verifies a bearer
// token outside Connect, a verifier error wrapping ErrKeySetUnavailable as the
// planes answer it: 503 with the bearerKeysUnavailable sentence, so the caller
// retries rather than re-authorizing, and an Error line with the cause, which
// is the server's dependency failing. It reports whether err was one; for any
// other error it writes and logs nothing, and the edge refuses the token its
// own way.
//
// No WWW-Authenticate is sent: a challenge tells an OAuth client its token is
// bad, which this token may not be.
func AnswerKeySetUnavailable(w http.ResponseWriter, l *zap.Logger, err error) bool {
	if !errors.Is(err, ErrKeySetUnavailable) {
		return false
	}
	l.Error(msgKeySetUnavailableLog, zap.Error(err))
	http.Error(w, bearerKeysUnavailable, http.StatusServiceUnavailable)
	return true
}
