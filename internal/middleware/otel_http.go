package middleware

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func OTelHTTP(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, OTelHandlerName)
}
