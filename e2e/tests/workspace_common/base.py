"""Shared base helpers for underpass-runtime E2E tests (gRPC transport)."""

from __future__ import annotations

import json
import os
import sys
import time
from datetime import datetime, timezone
from typing import Any
from urllib.error import HTTPError
from urllib.parse import urlparse
from urllib.request import Request, urlopen

import grpc
from google.protobuf import json_format, struct_pb2

# Add generated stubs to path (works both locally and inside container).
_gen_dir = os.path.join(os.path.dirname(__file__), "..", "..", "gen")
if not os.path.isdir(_gen_dir):
    _gen_dir = "/app/gen"  # container path
sys.path.insert(0, _gen_dir)

from underpass.runtime.v1 import runtime_pb2 as pb  # noqa: E402
from underpass.runtime.v1 import runtime_pb2_grpc as pb_grpc  # noqa: E402
from underpass.runtime.learning.v1 import learning_pb2 as lpb  # noqa: E402
from underpass.runtime.learning.v1 import learning_pb2_grpc as lpb_grpc  # noqa: E402

import ssl as _ssl  # noqa: E402

from .console import print_info, print_warning  # noqa: E402


def _env_bool(name: str, default: bool = False) -> bool:
    """Read a boolean from an environment variable (1/true/yes)."""
    val = os.getenv(name, "").strip().lower()
    if not val:
        return default
    return val in ("1", "true", "yes")


class WorkspaceE2EBase:
    """Reusable base class for workspace E2E tests (gRPC transport).

    Provides:
    - gRPC channel with optional TLS and auth metadata
    - session lifecycle helpers
    - tool invocation tracking
    - evidence recording and emission
    - backward-compatible request() for tests using raw HTTP-style calls

    Debug mode (E2E_DEBUG=1):
    - Logs every RPC call/response
    - Skips session cleanup so workspaces can be inspected
    """

    def __init__(
        self,
        *,
        test_id: str,
        run_id_prefix: str,
        workspace_url: str,
        evidence_file: str,
    ) -> None:
        self.test_id = test_id
        self.workspace_url = workspace_url.rstrip("/")
        self.evidence_file = evidence_file
        self.run_id = f"{run_id_prefix}-{int(time.time())}"

        self.debug = _env_bool("E2E_DEBUG")
        self.skip_cleanup = _env_bool("E2E_SKIP_CLEANUP") or self.debug

        self.sessions: list[str] = []
        self.invocation_counter = 0

        self.evidence: dict[str, Any] = {
            "test_id": self.test_id,
            "run_id": self.run_id,
            "status": "running",
            "started_at": self.now_iso(),
            "workspace_url": self.workspace_url,
            "debug": self.debug,
            "skip_cleanup": self.skip_cleanup,
            "steps": [],
            "sessions": [],
            "invocations": [],
        }

        # Build gRPC channel.
        self._metadata = self._build_auth_metadata()
        self._channel = self._build_channel()
        self._session_stub = pb_grpc.SessionServiceStub(self._channel)
        self._catalog_stub = pb_grpc.CapabilityCatalogServiceStub(self._channel)
        self._invocation_stub = pb_grpc.InvocationServiceStub(self._channel)
        self._health_stub = pb_grpc.HealthServiceStub(self._channel)
        self._learning_stub = lpb_grpc.LearningEvidenceServiceStub(self._channel)

        if self.debug:
            print_info("DEBUG MODE — cleanup disabled, verbose logging")
            print_info(f"  test_id:       {self.test_id}")
            print_info(f"  run_id:        {self.run_id}")
            print_info(f"  workspace_url: {self.workspace_url}")

    def _build_channel(self) -> grpc.Channel:
        """Build gRPC channel from WORKSPACE_URL."""
        target = self.workspace_url
        # Strip protocol prefix if present.
        for prefix in ("https://", "http://", "grpc://"):
            if target.startswith(prefix):
                target = target[len(prefix):]

        tls_ca = os.getenv("WORKSPACE_TLS_CA_PATH", "").strip()
        tls_cert = os.getenv("WORKSPACE_TLS_CERT_PATH", "").strip()
        tls_key = os.getenv("WORKSPACE_TLS_KEY_PATH", "").strip()

        if tls_ca or self.workspace_url.startswith("https://"):
            root_certs = None
            if tls_ca:
                with open(tls_ca, "rb") as f:
                    root_certs = f.read()
            private_key = None
            cert_chain = None
            if tls_cert and tls_key:
                with open(tls_key, "rb") as f:
                    private_key = f.read()
                with open(tls_cert, "rb") as f:
                    cert_chain = f.read()
            creds = grpc.ssl_channel_credentials(
                root_certificates=root_certs,
                private_key=private_key,
                certificate_chain=cert_chain,
            )
            return grpc.secure_channel(target, creds)
        return grpc.insecure_channel(target)

    def metrics_url(self, path: str = "/metrics") -> str:
        """Return the plaintext metrics endpoint derived from WORKSPACE_URL."""
        raw = self.workspace_url
        if "://" not in raw:
            raw = "https://" + raw
        parsed = urlparse(raw)
        host = parsed.hostname or "underpass-runtime"
        return f"http://{host}:9090{path}"

    def get_metrics(self, path: str = "/metrics", timeout: int = 10) -> tuple[int, str]:
        """Fetch the plaintext metrics endpoint."""
        req = Request(self.metrics_url(path), method="GET")
        try:
            with urlopen(req, timeout=timeout) as resp:
                body = resp.read().decode("utf-8", errors="replace")
                return resp.status, body
        except HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            return exc.code, body

    @staticmethod
    def _build_auth_metadata() -> list[tuple[str, str]]:
        """Build gRPC metadata for auth (mirrors HTTP trusted headers)."""
        token = os.getenv("WORKSPACE_AUTH_TOKEN", "").strip()
        if not token:
            return []
        return [
            (os.getenv("WORKSPACE_AUTH_TOKEN_HEADER", "x-workspace-auth-token").lower(), token),
            (os.getenv("WORKSPACE_AUTH_TENANT_HEADER", "x-workspace-tenant-id").lower(),
             os.getenv("WORKSPACE_AUTH_TENANT_ID", "e2e-tenant")),
            (os.getenv("WORKSPACE_AUTH_ACTOR_HEADER", "x-workspace-actor-id").lower(),
             os.getenv("WORKSPACE_AUTH_ACTOR_ID", "e2e-workspace")),
            (os.getenv("WORKSPACE_AUTH_ROLES_HEADER", "x-workspace-roles").lower(),
             os.getenv("WORKSPACE_AUTH_ROLES", "developer,devops")),
        ]

    @staticmethod
    def now_iso() -> str:
        return datetime.now(timezone.utc).isoformat()

    @staticmethod
    def _normalize_dict(d: dict[str, Any]) -> dict[str, Any]:
        """Normalize a protobuf MessageToDict output for backward compatibility.

        - Converts camelCase keys to snake_case (sessionId → session_id)
        - Strips INVOCATION_STATUS_ prefix from status values
        - Recurses into nested dicts
        """
        import re

        def to_snake(name: str) -> str:
            return re.sub(r"(?<=[a-z0-9])([A-Z])", r"_\1", name).lower()

        def normalize_value(v: Any) -> Any:
            if isinstance(v, dict):
                return {to_snake(k): normalize_value(val) for k, val in v.items()}
            if isinstance(v, list):
                return [normalize_value(item) for item in v]
            if isinstance(v, str) and v.startswith("INVOCATION_STATUS_"):
                return v.replace("INVOCATION_STATUS_", "").lower()
            return v

        return normalize_value(d)  # type: ignore[return-value]

    # ─── NATS TLS helper ─────────────────────────────────────────────────

    def build_nats_tls(self) -> _ssl.SSLContext | None:
        """Build an SSL context for nats-py from mounted certs.

        Reads the same e2e-client-tls volume used for gRPC. Returns None if
        the required files are not available (allows graceful fallback).
        """
        ca = os.getenv("WORKSPACE_TLS_CA_PATH", "").strip()
        cert = os.getenv("WORKSPACE_TLS_CERT_PATH", "").strip()
        key = os.getenv("WORKSPACE_TLS_KEY_PATH", "").strip()

        if not (ca and os.path.isfile(ca)):
            return None

        ctx = _ssl.create_default_context(purpose=_ssl.Purpose.SERVER_AUTH, cafile=ca)
        if cert and key and os.path.isfile(cert) and os.path.isfile(key):
            ctx.load_cert_chain(certfile=cert, keyfile=key)
        return ctx

    async def nats_connect(self):
        """Connect to NATS with mTLS. Raises on failure (no fallback)."""
        import nats as nats_py

        nats_url = os.getenv("NATS_URL", "nats://nats:4222").strip()
        tls_ctx = self.build_nats_tls()
        nc = await nats_py.connect(nats_url, tls=tls_ctx)
        return nc

    async def jetstream_subscribe(self, nc, subject: str, callback):
        """Subscribe to a JetStream subject using an ordered push consumer.

        Returns the subscription. The callback receives nats messages.
        JetStream is required because the runtime publishes via JetStream
        (with stream dedup), and core NATS subscribers don't receive
        stream-captured messages.
        """
        js = nc.jetstream()
        sub = await js.subscribe(subject, ordered_consumer=True)
        return sub

    # ─── gRPC call helpers ───────────────────────────────────────────────

    def _call(self, method, request, timeout: int = 60):
        """Execute a gRPC call with auth metadata and timeout."""
        return method(request, metadata=self._metadata, timeout=timeout)

    # ─── Backward-compatible request() ───────────────────────────────────
    #
    # Many E2E tests call self.request("GET", "/v1/sessions/{id}/tools").
    # This translates HTTP-style calls to gRPC RPCs so tests don't need
    # rewriting.

    def request(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        timeout: int = 60,
    ) -> tuple[int, dict[str, Any]]:
        """Translate HTTP-style request to gRPC call.

        Returns (status_code, response_dict) for backward compatibility.
        200 on success, gRPC error code mapped to HTTP equivalent on failure.
        """
        if self.debug:
            print_info(f"  >> {method} {path}")

        try:
            result = self._dispatch(method, path, payload, timeout)
            if self.debug:
                print_info(f"  << 200")
            return 200 if method != "POST" or "invoke" in path else 200, result
        except grpc.RpcError as e:
            code = e.code()
            http_status = _grpc_to_http(code)
            if self.debug:
                print_info(f"  << {http_status} ({code.name}: {e.details()})")
            return http_status, {"error": {"code": code.name.lower(), "message": e.details()}}

    def _dispatch(self, method: str, path: str, payload: dict | None, timeout: int) -> dict:
        """Route HTTP-style path to the correct gRPC RPC."""
        # Health
        if path == "/healthz":
            resp = self._call(self._health_stub.Check, pb.CheckRequest(), timeout)
            return {"status": resp.status}

        # POST /v1/sessions
        if method == "POST" and path == "/v1/sessions":
            return self._rpc_create_session(payload or {}, timeout)

        # DELETE /v1/sessions/{id}
        if method == "DELETE" and path.startswith("/v1/sessions/"):
            session_id = path.split("/v1/sessions/")[1].split("/")[0]
            resp = self._call(self._session_stub.CloseSession,
                              pb.CloseSessionRequest(session_id=session_id), timeout)
            return {"closed": resp.closed}

        # GET /v1/sessions/{id}/tools
        if path.endswith("/tools") and "/v1/sessions/" in path:
            session_id = path.split("/v1/sessions/")[1].split("/")[0]
            resp = self._call(self._catalog_stub.ListTools,
                              pb.ListToolsRequest(session_id=session_id), timeout)
            return {"tools": [json_format.MessageToDict(t) for t in resp.tools]}

        # GET /v1/sessions/{id}/tools/discovery[?detail=full&risk=low&...]
        if "/tools/discovery" in path:
            session_id = path.split("/v1/sessions/")[1].split("/")[0]
            # Parse query params from path
            params: dict[str, str] = {}
            if "?" in path:
                qs = path.split("?", 1)[1]
                for pair in qs.split("&"):
                    k, _, v = pair.partition("=")
                    params[k] = v
            detail_str = params.get("detail", "compact")
            detail = (
                pb.DiscoveryDetail.DISCOVERY_DETAIL_FULL
                if detail_str == "full"
                else pb.DiscoveryDetail.DISCOVERY_DETAIL_COMPACT
            )
            req = pb.DiscoverToolsRequest(
                session_id=session_id,
                detail=detail,
                risk=[params["risk"]] if "risk" in params else [],
                cost=[params["cost"]] if "cost" in params else [],
                side_effects=[params["side_effects"]] if "side_effects" in params else [],
                scope=[params["scope"]] if "scope" in params else [],
            )
            resp = self._call(self._catalog_stub.DiscoverTools, req, timeout)
            tools = []
            if resp.HasField("compact"):
                tools = [json_format.MessageToDict(t) for t in resp.compact.tools]
            elif resp.HasField("full"):
                tools = [json_format.MessageToDict(t) for t in resp.full.tools]
            return {"tools": tools, "total": resp.total, "filtered": resp.filtered}

        # GET /v1/sessions/{id}/tools/recommendations[?task_hint=...&top_k=...]
        if "/tools/recommendations" in path:
            session_id = path.split("/v1/sessions/")[1].split("/")[0]
            params: dict[str, str] = {}
            if "?" in path:
                qs = path.split("?", 1)[1]
                for pair in qs.split("&"):
                    k, _, v = pair.partition("=")
                    params[k] = v
            task_hint = params.get("task_hint", "").replace("+", " ")
            top_k = int(params.get("top_k", "10"))
            req = pb.RecommendToolsRequest(session_id=session_id, task_hint=task_hint, top_k=top_k)
            resp = self._call(self._catalog_stub.RecommendTools, req, timeout)
            result = {
                "recommendations": [json_format.MessageToDict(r) for r in resp.recommendations],
                "task_hint": resp.task_hint,
                "top_k": resp.top_k,
            }
            # Bridge fields (P0 learning evidence).
            if resp.recommendation_id:
                result["recommendation_id"] = resp.recommendation_id
                result["event_id"] = resp.event_id
                result["event_subject"] = resp.event_subject
                result["decision_source"] = resp.decision_source
                result["algorithm_id"] = resp.algorithm_id
                result["algorithm_version"] = resp.algorithm_version
                result["policy_mode"] = resp.policy_mode
            return result

        # POST /v1/sessions/{id}/tools/{name}/invoke
        if method == "POST" and "/tools/" in path and "/invoke" in path:
            parts = path.split("/")
            session_id = parts[3]
            tool_name = parts[5]
            return self._rpc_invoke(session_id, tool_name, payload or {}, timeout)

        # GET /v1/invocations/{id}
        if path.startswith("/v1/invocations/"):
            inv_id = path.split("/v1/invocations/")[1].split("/")[0]
            sub = path.split(inv_id)[-1].strip("/")

            if sub == "logs":
                resp = self._call(self._invocation_stub.GetInvocationLogs,
                                  pb.GetInvocationLogsRequest(invocation_id=inv_id), timeout)
                return {"logs": [json_format.MessageToDict(l) for l in resp.logs]}

            if sub == "artifacts":
                resp = self._call(self._invocation_stub.GetInvocationArtifacts,
                                  pb.GetInvocationArtifactsRequest(invocation_id=inv_id), timeout)
                return {"artifacts": [json_format.MessageToDict(a) for a in resp.artifacts]}

            resp = self._call(self._invocation_stub.GetInvocation,
                              pb.GetInvocationRequest(invocation_id=inv_id), timeout)
            return {"invocation": self._normalize_dict(json_format.MessageToDict(resp.invocation))}

        # GET /v1/learning/recommendations/{recommendation_id}
        if path.startswith("/v1/learning/recommendations/") and "/events" not in path:
            rec_id = path.split("/v1/learning/recommendations/")[1].split("/")[0]
            req = lpb.GetRecommendationDecisionRequest(recommendation_id=rec_id)
            resp = self._call(self._learning_stub.GetRecommendationDecision, req, timeout)
            return self._normalize_dict(json_format.MessageToDict(resp.decision))

        # GET /v1/learning/evidence/recommendations/{recommendation_id}
        if path.startswith("/v1/learning/evidence/recommendations/"):
            rec_id = path.split("/v1/learning/evidence/recommendations/")[1].split("/")[0]
            req = lpb.GetEvidenceBundleRequest(recommendation_id=rec_id)
            resp = self._call(self._learning_stub.GetEvidenceBundle, req, timeout)
            return self._normalize_dict(json_format.MessageToDict(resp.bundle))

        raise ValueError(f"Unknown route: {method} {path}")

    def _rpc_create_session(self, payload: dict, timeout: int) -> dict:
        principal = payload.get("principal", {})
        req = pb.CreateSessionRequest(
            session_id=payload.get("session_id", ""),
            repo_url=payload.get("repo_url", ""),
            repo_ref=payload.get("repo_ref", ""),
            source_repo_path=payload.get("source_repo_path", ""),
            allowed_paths=payload.get("allowed_paths", []),
            principal=pb.Principal(
                tenant_id=principal.get("tenant_id", ""),
                actor_id=principal.get("actor_id", ""),
                roles=principal.get("roles", []),
            ),
            metadata=payload.get("metadata", {}),
            expires_in_seconds=payload.get("expires_in_seconds", 0),
        )
        resp = self._call(self._session_stub.CreateSession, req, timeout)
        return {"session": self._normalize_dict(json_format.MessageToDict(resp.session))}

    def _rpc_invoke(self, session_id: str, tool_name: str, payload: dict, timeout: int) -> dict:
        args_dict = payload.get("args", {})
        args_struct = struct_pb2.Struct()
        args_struct.update(args_dict)

        req = pb.InvokeToolRequest(
            session_id=session_id,
            tool_name=tool_name,
            correlation_id=payload.get("correlation_id", ""),
            args=args_struct,
            approved=payload.get("approved", False),
        )
        resp = self._call(self._invocation_stub.InvokeTool, req, timeout)
        result = {"invocation": self._normalize_dict(json_format.MessageToDict(resp.invocation))}
        # Include error at top level if present (for backward compat with HTTP).
        if resp.invocation.HasField("error"):
            result["error"] = {
                "code": resp.invocation.error.code,
                "message": resp.invocation.error.message,
            }
        return result

    # ─── High-level helpers (unchanged API) ──────────────────────────────

    def record_step(self, name: str, status: str, data: Any | None = None) -> None:
        entry: dict[str, Any] = {"at": self.now_iso(), "step": name, "status": status}
        if data is not None:
            entry["data"] = data
        self.evidence["steps"].append(entry)

    def extract_error(self, invocation: dict[str, Any] | None, body: dict[str, Any]) -> dict[str, Any]:
        if isinstance(invocation, dict) and isinstance(invocation.get("error"), dict):
            return invocation["error"]
        if isinstance(body.get("error"), dict):
            return body["error"]
        return {}

    def record_invocation(
        self,
        *,
        session_id: str,
        tool: str,
        http_status: int,
        invocation: dict[str, Any] | None,
        body: dict[str, Any],
    ) -> None:
        error = self.extract_error(invocation, body)
        self.evidence["invocations"].append(
            {
                "at": self.now_iso(),
                "session_id": session_id,
                "tool": tool,
                "http_status": http_status,
                "invocation_id": invocation.get("id") if isinstance(invocation, dict) else None,
                "invocation_status": invocation.get("status") if isinstance(invocation, dict) else None,
                "error_code": error.get("code"),
                "error_message": error.get("message"),
            }
        )

    def create_session(
        self,
        *,
        payload: dict[str, Any],
        session_record: dict[str, Any] | None = None,
    ) -> str:
        status, body = self.request("POST", "/v1/sessions", payload)
        if status != 200:
            raise RuntimeError(f"create session failed ({status}): {body}")

        session_id = str(body.get("session", {}).get("id", "")).strip()
        if not session_id:
            raise RuntimeError(f"create session missing id: {body}")

        self.sessions.append(session_id)
        entry: dict[str, Any] = {"at": self.now_iso(), "session_id": session_id}
        if session_record is not None:
            entry.update(session_record)
        else:
            entry["payload"] = payload

        workspace_path = body.get("session", {}).get("workspace_path", "")
        if workspace_path:
            entry["workspace_path"] = workspace_path

        self.evidence["sessions"].append(entry)

        if self.debug:
            print_info(f"  SESSION created: {session_id}")
            if workspace_path:
                print_info(f"    workspace_path: {workspace_path}")
        return session_id

    def invoke(
        self,
        *,
        session_id: str,
        tool_name: str,
        args: dict[str, Any],
        approved: bool = False,
        timeout: int = 120,
    ) -> tuple[int, dict[str, Any], dict[str, Any] | None]:
        self.invocation_counter += 1
        payload = {
            "correlation_id": f"{self.run_id}-{self.invocation_counter:04d}",
            "approved": approved,
            "args": args,
        }
        status, body = self.request(
            "POST",
            f"/v1/sessions/{session_id}/tools/{tool_name}/invoke",
            payload,
            timeout=timeout,
        )
        invocation = body.get("invocation") if isinstance(body, dict) else None
        self.record_invocation(
            session_id=session_id,
            tool=tool_name,
            http_status=status,
            invocation=invocation if isinstance(invocation, dict) else None,
            body=body if isinstance(body, dict) else {},
        )

        if self.debug and isinstance(invocation, dict):
            inv_id = invocation.get("id", "?")
            inv_status = invocation.get("status", "?")
            duration = invocation.get("duration_ms", "?")
            print_info(f"  INVOKE {tool_name} -> {inv_status} (id={inv_id}, {duration}ms)")

        return status, body, invocation if isinstance(invocation, dict) else None

    def assert_invocation_succeeded(
        self,
        *,
        invocation: dict[str, Any] | None,
        body: dict[str, Any],
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation")
        status = str(invocation.get("status", "")).strip()
        if status != "succeeded":
            error = self.extract_error(invocation, body)
            code = str(error.get("code", "")).strip()
            raise RuntimeError(f"{label}: expected succeeded, got {status} ({code})")

    def assert_policy_denied(
        self,
        *,
        status: int,
        body: dict[str, Any],
        invocation: dict[str, Any] | None,
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation in response body")

        code = str(self.extract_error(invocation, body).get("code", "")).strip()
        if code != "policy_denied":
            raise RuntimeError(
                f"{label}: expected policy_denied, got code={code}, status={status}, body={body}"
            )

    def assert_not_policy_denied(
        self,
        *,
        status: int,
        body: dict[str, Any],
        invocation: dict[str, Any] | None,
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation in response body")

        code = str(self.extract_error(invocation, body).get("code", "")).strip()
        if code in ("policy_denied", "approval_required"):
            raise RuntimeError(f"{label}: unexpected policy block ({code})")

        inv_status = str(invocation.get("status", "")).strip()
        if inv_status != "succeeded":
            raise RuntimeError(
                f"{label}: expected succeeded invocation, got status={inv_status}, status={status}, body={body}"
            )

    def write_evidence(self, status: str, error_message: str = "") -> None:
        self.evidence["status"] = status
        self.evidence["ended_at"] = self.now_iso()
        if error_message:
            self.evidence["error_message"] = error_message

        if self.debug:
            self._print_debug_summary()

        try:
            with open(self.evidence_file, "w", encoding="utf-8") as handle:
                json.dump(self.evidence, handle, ensure_ascii=False, indent=2)
            print_warning(f"Evidence file: {self.evidence_file}")
        except Exception as exc:
            print_warning(f"Could not write evidence file: {exc}")

        print("EVIDENCE_JSON_START")
        print(json.dumps(self.evidence, ensure_ascii=False, indent=2))
        print("EVIDENCE_JSON_END")

    def _print_debug_summary(self) -> None:
        from .console import Colors
        sep = f"{Colors.BLUE}{'─' * 72}{Colors.NC}"
        print(f"\n{sep}")
        print(f"{Colors.BLUE}  DEBUG SUMMARY — {self.test_id} ({self.run_id}){Colors.NC}")
        print(sep)

        print(f"\n  Sessions ({len(self.sessions)}):")
        for s in self.evidence.get("sessions", []):
            sid = s.get("session_id", "?")
            wp = s.get("workspace_path", "(no path)")
            print(f"    {sid}  ->  {wp}")

        invocations = self.evidence.get("invocations", [])
        print(f"\n  Invocations ({len(invocations)}):")
        for inv in invocations:
            iid = inv.get("invocation_id") or "—"
            tool = inv.get("tool", "?")
            ist = inv.get("invocation_status") or inv.get("error_code") or "?"
            http = inv.get("http_status", "?")
            print(f"    {iid}  {tool:30s}  {ist:12s}  HTTP {http}")

        if self.skip_cleanup:
            print(f"\n  {Colors.YELLOW}Sessions left open for inspection.{Colors.NC}")

        print(f"\n{sep}\n")

    def cleanup_sessions(self) -> None:
        if self.skip_cleanup:
            print_warning(
                f"SKIP CLEANUP — {len(self.sessions)} session(s) left open for inspection: "
                + ", ".join(self.sessions)
            )
            return
        for session_id in self.sessions:
            try:
                self.request("DELETE", f"/v1/sessions/{session_id}")
            except Exception:
                pass


def _grpc_to_http(code: grpc.StatusCode) -> int:
    """Map gRPC status code to HTTP status code for backward compatibility."""
    return {
        grpc.StatusCode.OK: 200,
        grpc.StatusCode.INVALID_ARGUMENT: 400,
        grpc.StatusCode.UNAUTHENTICATED: 401,
        grpc.StatusCode.PERMISSION_DENIED: 403,
        grpc.StatusCode.NOT_FOUND: 404,
        grpc.StatusCode.FAILED_PRECONDITION: 428,
        grpc.StatusCode.DEADLINE_EXCEEDED: 504,
        grpc.StatusCode.INTERNAL: 500,
        grpc.StatusCode.UNAVAILABLE: 503,
    }.get(code, 500)
