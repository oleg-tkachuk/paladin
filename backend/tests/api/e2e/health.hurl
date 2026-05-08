# Liveness / readiness / startup probes.
#
# These three endpoints are HTTP/1.1 plain-text — they're consumed by the
# k8s probes, not by Connect clients.  All three return body "ok" with
# 200 when healthy.

GET {{host}}/livez
HTTP 200
[Asserts]
body == "ok"

GET {{host}}/startupz
HTTP 200
[Asserts]
body == "ok"

GET {{host}}/readyz
HTTP 200
[Asserts]
body == "ok"
