package paladin_test

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
)

// connectHandler serves what register puts on a Connect server, as one
// http.Handler a test can wrap to look at each request first.
func connectHandler(register func(*connect.Server)) http.Handler {
	server := connect.NewServer()
	register(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return mux
}
