package session

import (
	"net/http"
	"time"
)

// CookieName names the session cookie. The __Host- prefix makes the browser
// refuse it unless it is Secure, host-only and on path "/".
const CookieName = "__Host-paladin_session"

// cookiePath is the path a __Host- cookie must carry.
const cookiePath = "/"

// setCookie stores refresh in the session cookie for ttl.
func setCookie(w http.ResponseWriter, refresh string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    refresh,
		Path:     cookiePath,
		MaxAge:   int(ttl / time.Second),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCookie tells the browser to drop the session cookie.
func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// refreshToken is the refresh token the request's session cookie carries,
// or "" when it carries none.
func refreshToken(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
