"""connectrpc's types as strict checking can read them.

connectrpc's ``Interceptor`` and ``InterceptorSync`` aliases name
``MetadataInterceptor`` without its type argument, and its ``RequestContext``
is generic over messages an interceptor never sees, so a strict checker reads
both as partially unknown wherever they appear. These spell them out once.
"""

from __future__ import annotations

from typing import Any

from connectrpc.interceptor import (
    BidiStreamInterceptor,
    BidiStreamInterceptorSync,
    ClientStreamInterceptor,
    ClientStreamInterceptorSync,
    MetadataInterceptor,
    MetadataInterceptorSync,
    ServerStreamInterceptor,
    ServerStreamInterceptorSync,
    UnaryInterceptor,
    UnaryInterceptorSync,
)
from connectrpc.request import RequestContext

type Context = RequestContext[Any, Any]
"""The context of any call, whatever its messages."""

type InterceptorSync = (
    UnaryInterceptorSync
    | ClientStreamInterceptorSync
    | ServerStreamInterceptorSync
    | BidiStreamInterceptorSync
    | MetadataInterceptorSync[Any]
)
"""Any interceptor a generated ``…ClientSync`` takes."""

type Interceptor = (
    UnaryInterceptor
    | ClientStreamInterceptor
    | ServerStreamInterceptor
    | BidiStreamInterceptor
    | MetadataInterceptor[Any]
)
"""Any interceptor a generated async ``…Client`` takes."""
