"""Certificates for the TLS tests, made with ``cryptography``."""

from __future__ import annotations

import datetime
import ipaddress
from dataclasses import dataclass
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

# Valid for an hour, which outlasts any run.
LIFETIME = datetime.timedelta(hours=1)
LOOPBACK = ipaddress.IPv4Address("127.0.0.1")


def _pem(cert: x509.Certificate) -> bytes:
    return cert.public_bytes(serialization.Encoding.PEM)


def _key_pem(key: ec.EllipticCurvePrivateKey) -> bytes:
    return key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )


# What an X.509-SVID leaf carries, and nothing a CA would.
_LEAF_USAGE = x509.KeyUsage(
    digital_signature=True,
    key_cert_sign=False,
    crl_sign=False,
    content_commitment=False,
    key_encipherment=False,
    data_encipherment=False,
    key_agreement=False,
    encipher_only=False,
    decipher_only=False,
)


@dataclass
class Leaf:
    cert_pem: bytes
    key_pem: bytes
    serial: int


class Authority:
    def __init__(self, name: str = "test CA") -> None:
        self.key = ec.generate_private_key(ec.SECP256R1())
        self.name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)])
        now = datetime.datetime.now(datetime.UTC)
        self.cert = (
            x509.CertificateBuilder()
            .subject_name(self.name)
            .issuer_name(self.name)
            .public_key(self.key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - datetime.timedelta(minutes=1))
            .not_valid_after(now + LIFETIME)
            .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
            .add_extension(
                x509.KeyUsage(
                    digital_signature=True,
                    key_cert_sign=True,
                    crl_sign=True,
                    content_commitment=False,
                    key_encipherment=False,
                    data_encipherment=False,
                    key_agreement=False,
                    encipher_only=False,
                    decipher_only=False,
                ),
                critical=True,
            )
            .add_extension(
                x509.SubjectKeyIdentifier.from_public_key(self.key.public_key()), critical=False
            )
            .sign(self.key, hashes.SHA256())
        )
        self.pem = _pem(self.cert)

    def issue(
        self,
        *,
        client: bool,
        ips: bool = True,
        dns: tuple[str, ...] = (),
        uris: tuple[str, ...] = (),
    ) -> Leaf:
        """A leaf for a client or a server; ``uris`` makes it an X.509-SVID."""
        key = ec.generate_private_key(ec.SECP256R1())
        now = datetime.datetime.now(datetime.UTC)
        names: list[x509.GeneralName] = [x509.DNSName(d) for d in dns]
        names += [x509.UniformResourceIdentifier(u) for u in uris]
        if ips:
            names.append(x509.IPAddress(LOOPBACK))
        usage = ExtendedKeyUsageOID.CLIENT_AUTH if client else ExtendedKeyUsageOID.SERVER_AUTH
        builder = (
            x509.CertificateBuilder()
            .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "test leaf")]))
            .issuer_name(self.name)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - datetime.timedelta(minutes=1))
            .not_valid_after(now + LIFETIME)
            .add_extension(x509.ExtendedKeyUsage([usage]), critical=False)
            .add_extension(_LEAF_USAGE, critical=True)
            .add_extension(
                x509.AuthorityKeyIdentifier.from_issuer_public_key(self.key.public_key()),
                critical=False,
            )
        )
        if names:
            builder = builder.add_extension(x509.SubjectAlternativeName(names), critical=False)
        cert = builder.sign(self.key, hashes.SHA256())
        return Leaf(_pem(cert), _key_pem(key), cert.serial_number)


@dataclass
class Files:
    ca: Path
    cert: Path
    key: Path

    def write_client(self, leaf: Leaf) -> None:
        self.cert.write_bytes(leaf.cert_pem)
        self.key.write_bytes(leaf.key_pem)


def write(directory: Path, ca: bytes, client: Leaf) -> Files:
    files = Files(directory / "ca.pem", directory / "tls.crt", directory / "tls.key")
    files.ca.write_bytes(ca)
    files.write_client(client)
    return files
