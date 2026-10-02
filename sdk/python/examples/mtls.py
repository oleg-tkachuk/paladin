"""Mutual TLS with a workload identity whose certificates rotate on disk.

The files are re-read when they change, so a certificate the agent rotates is
in use from the next connection, with no restart. The Python SDK checks the
server against the CA bundle and its host name; a SPIFFE ID check is Go-only
(see the parity table in the README).
"""

import paladin

BUNDLE = "/var/run/secrets/spiffe/bundle.pem"
SVID = "/var/run/secrets/spiffe/svid.pem"
SVID_KEY = "/var/run/secrets/spiffe/svid-key.pem"


def build() -> paladin.Paladin:
    identity = paladin.TLS(ca_file=BUNDLE, cert_file=SVID, key_file=SVID_KEY, reload_interval=30.0)
    return paladin.connect(
        paladin.Endpoints(data="https://paladin-data.paladin.svc:8443"),
        tls=identity,  # the RPCs
        transfer=paladin.Transfer(tls=identity),  # and storage, when it wants the same
    )


if __name__ == "__main__":
    build()
