"""connect: a client for every service of each plane, each with its audience."""

from __future__ import annotations

import importlib.util
import io
import threading
from collections.abc import Iterator
from pathlib import Path
from wsgiref.simple_server import WSGIRequestHandler, make_server

import pytest
from connectrpc.errors import ConnectError

from paladin import (
    AUDIENCE_DATA,
    AUDIENCE_IAM,
    Endpoints,
    connect,
    connect_async,
    facade,
)
from paladin.data.v1 import object_service_pb2
from paladin.iam.v1 import health_service_pb2

SCRIPTS = Path(__file__).resolve().parents[1] / "scripts"
URL = "https://paladin.example"
_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0


def _generator():  # type: ignore[no-untyped-def]
    spec = importlib.util.spec_from_file_location("gen_facade", SCRIPTS / "gen_facade.py")
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_facade_is_generated() -> None:
    gen = _generator()
    assert Path(facade.__file__).read_text() == gen.render(), (
        "facade.py is stale: run scripts/gen_facade.py"
    )


@pytest.mark.parametrize("make", [connect, connect_async])
def test_connect_builds_every_service_of_every_plane(make) -> None:  # type: ignore[no-untyped-def]
    gen = _generator()
    p = make(Endpoints(data=URL, admin=URL, iam=URL))
    planes = {"Data": p.data, "Admin": p.admin, "IAM": p.iam}
    for name, services in facade.SERVICES.items():
        for service in services:
            assert getattr(planes[name], gen.attribute(service), None) is not None, (
                f"{name} lacks {service}"
            )


def test_attribute_names() -> None:
    gen = _generator()
    assert gen.attribute("APITokenService") == "api_token"
    assert gen.attribute("MultipartUploadService") == "multipart_upload"
    assert gen.attribute("MCPInspectService") == "mcp_inspect"


def test_connect_leaves_out_planes_without_a_url() -> None:
    p = connect(Endpoints(data=URL))
    assert p.data is not None and p.admin is None and p.iam is None
    with pytest.raises(ValueError):
        Endpoints()


class _PerAudience:
    def token(self, audience: str) -> str:
        return f"tok-{audience}"


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


@pytest.fixture
def recorder() -> Iterator[tuple[str, dict[str, str]]]:
    seen: dict[str, str] = {}

    def app(environ, start_response):  # type: ignore[no-untyped-def]
        length = int(environ.get("CONTENT_LENGTH") or 0)
        io.BytesIO(environ["wsgi.input"].read(length))
        seen[environ["PATH_INFO"]] = environ.get("HTTP_AUTHORIZATION", "")
        start_response("404 Not Found", [("Content-Type", "text/plain")])
        return [b""]

    httpd = make_server(_LOOPBACK, _EPHEMERAL_PORT, app, handler_class=_Quiet)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    try:
        yield f"http://{_LOOPBACK}:{httpd.server_port}", seen
    finally:
        httpd.shutdown()
        httpd.server_close()


def test_each_plane_gets_its_own_audience(recorder) -> None:  # type: ignore[no-untyped-def]
    url, seen = recorder
    p = connect(Endpoints(data=url, iam=url), token_source=_PerAudience())
    for call in (
        lambda: p.iam.health.get_version(health_service_pb2.GetVersionRequest()),
        lambda: p.data.object.get_object(object_service_pb2.GetObjectRequest()),
    ):
        # The recorder answers 404; only the header it saw matters.
        with pytest.raises(ConnectError):
            call()
    assert seen["/paladin.iam.v1.HealthService/GetVersion"] == f"Bearer tok-{AUDIENCE_IAM}"
    assert seen["/paladin.data.v1.ObjectService/GetObject"] == f"Bearer tok-{AUDIENCE_DATA}"
