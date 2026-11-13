package contract_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oleg-tkachuk/paladin/frontend/server/contract"
)

func TestLoad(t *testing.T) {
	n, err := contract.Load()
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(n.SessionCookie, "__Host-"),
		"the session role sets a __Host- cookie; a different name gates on nothing")
	for _, p := range []string{n.LoginPath, n.BannerPath, n.LivePath, n.ConsentPath, n.AuthorizePath} {
		assert.True(t, strings.HasPrefix(p, "/"), "%q is a path", p)
	}
	assert.Contains(t, n.PublicPrefixes, n.LoginPath, "the login page needs no session")

	public := func(p string) bool {
		for _, pre := range n.PublicPrefixes {
			if strings.HasPrefix(p, pre) {
				return true
			}
		}
		return false
	}
	assert.True(t, public(n.LivePath), "kubelet probes without a cookie")
	assert.True(t, public(n.ConsentPath), "consent is a pre-login flow")
	assert.True(t, public(n.AuthorizePath), "consent posts before login")
	assert.False(t, public(n.BannerPath), "the banner is shown inside the shell")
}
