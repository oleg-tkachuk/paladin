// Package session is the console's session role, `serve session` (ADR-0048).
//
// It holds the console's session: the refresh token lives in an httpOnly
// cookie it alone sets and reads, and the access token each plane needs is
// minted from it at the iam plane, kept here until it expires, and attached
// to the console's calls as this role forwards them. The browser holds no
// token, and the role holds no key: the planes verify what it attaches as
// they verify any other caller.
//
// Connect calls are forwarded as bytes, routed by the proto package in their
// path, so a new RPC reaches the console without a change here. The IAM
// procedures that hand out tokens are not forwarded: the browser reaches
// them only through this role's own session endpoints.
package session
