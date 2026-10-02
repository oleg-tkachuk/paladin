"""Presigned URLs signed for a public storage host, sent in-cluster.

Storage signs its URLs for the host the outside world reaches; inside the
cluster it answers at another address. The Transfer sends them there, with
the Host header they were signed for — the signature covers the header, not
the address.
"""

import paladin


def build() -> paladin.Paladin:
    transfer = paladin.Transfer(
        split_horizon=("https://s3.example.com", "http://seaweedfs-s3.storage.svc:8333"),
    )
    return paladin.connect(
        paladin.Endpoints(data="https://paladin-data.example.com"),
        token_source=paladin.StaticToken("paladin_pat_…"),
        transfer=transfer,
    )


if __name__ == "__main__":
    build()
