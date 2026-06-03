"""E2E test: runtime saturation + notify specialist tools via Kubernetes Job."""

from __future__ import annotations

import os
import sys
import textwrap
import time
from urllib.parse import urlparse

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"
DEFAULT_NAMESPACE = "underpass-runtime"
DEFAULT_FIXTURE_IMAGE = os.getenv(
    "E2E_RUNTIME_FIXTURE_IMAGE",
    "docker.io/library/python:3.13-slim",
)

SATURATION_PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-saturation-actor",
    "roles": ["devops", "platform_admin"],
}

NOTIFY_PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-notify-actor",
    "roles": ["incident_communicator"],
}


class RuntimeSaturationNotifyToolsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="24-runtime-saturation-notify-tools",
            run_id_prefix="e2e-saturation-notify",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-24-{int(time.time())}.json"),
        )
        self.namespace = os.getenv(
            "K8S_TARGET_NAMESPACE",
            os.getenv("WORKSPACE_NAMESPACE", self._infer_namespace()),
        )
        suffix = self.run_id.rsplit("-", 1)[-1]
        self.target_name = f"e2e-saturation-{suffix}"
        self.downstream_name = f"e2e-downstream-{suffix}"
        self.notify_sink_service = os.getenv("E2E_NOTIFY_SINK_SERVICE", "underpass-runtime-notify-sink")
        self.notify_sink_deployment = f"{self.notify_sink_service}-server"
        self.fixture_image = os.getenv("E2E_RUNTIME_FIXTURE_IMAGE", DEFAULT_FIXTURE_IMAGE)
        self.saturation_metadata = {
            "tool_profile": "saturation-operator-bounded",
            "environment": "e2e",
            "runtime_environment": "e2e",
        }
        self.notify_metadata = {
            "tool_profile": "human-escalation-minimal",
            "environment": "e2e",
            "runtime_environment": "e2e",
        }

    def _infer_namespace(self) -> str:
        raw = self.workspace_url
        if "://" not in raw:
            raw = "https://" + raw
        parsed = urlparse(raw)
        host = parsed.hostname or ""
        parts = host.split(".")
        if len(parts) >= 2 and parts[1]:
            return parts[1]
        return DEFAULT_NAMESPACE

    def _fixture_manifest(self) -> str:
        return textwrap.dedent(
            f"""\
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: {self.target_name}
              namespace: {self.namespace}
              labels:
                app: {self.target_name}
                underpass.ai/test-id: "24"
                underpass.ai/run-id: "{self.run_id}"
            spec:
              replicas: 1
              revisionHistoryLimit: 2
              selector:
                matchLabels:
                  app: {self.target_name}
              template:
                metadata:
                  labels:
                    app: {self.target_name}
                    underpass.ai/test-id: "24"
                    underpass.ai/run-id: "{self.run_id}"
                spec:
                  containers:
                    - name: app
                      image: {self.fixture_image}
                      imagePullPolicy: IfNotPresent
                      command: ["python", "-c", "import time; time.sleep(3600)"]
                      ports:
                        - containerPort: 8080
            ---
            apiVersion: v1
            kind: Service
            metadata:
              name: {self.target_name}
              namespace: {self.namespace}
              labels:
                app: {self.target_name}
                underpass.ai/test-id: "24"
                underpass.ai/run-id: "{self.run_id}"
            spec:
              selector:
                app: {self.target_name}
              ports:
                - name: http
                  port: 8080
                  targetPort: 8080
            ---
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: {self.downstream_name}
              namespace: {self.namespace}
              labels:
                app: {self.downstream_name}
                underpass.ai/test-id: "24"
                underpass.ai/run-id: "{self.run_id}"
            spec:
              replicas: 1
              revisionHistoryLimit: 2
              selector:
                matchLabels:
                  app: {self.downstream_name}
              template:
                metadata:
                  labels:
                    app: {self.downstream_name}
                    underpass.ai/test-id: "24"
                    underpass.ai/run-id: "{self.run_id}"
                spec:
                  containers:
                    - name: app
                      image: {self.fixture_image}
                      imagePullPolicy: IfNotPresent
                      command: ["python", "-m", "http.server", "8080"]
                      ports:
                        - containerPort: 8080
            ---
            apiVersion: v1
            kind: Service
            metadata:
              name: {self.downstream_name}
              namespace: {self.namespace}
              labels:
                app: {self.downstream_name}
                underpass.ai/test-id: "24"
                underpass.ai/run-id: "{self.run_id}"
            spec:
              selector:
                app: {self.downstream_name}
              ports:
                - name: http
                  port: 8080
                  targetPort: 8080
            ---
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: {self.notify_sink_deployment}
              namespace: {self.namespace}
              labels:
                app: {self.notify_sink_service}
                underpass.ai/test-id: "24"
            spec:
              replicas: 1
              revisionHistoryLimit: 1
              selector:
                matchLabels:
                  app: {self.notify_sink_service}
              template:
                metadata:
                  labels:
                    app: {self.notify_sink_service}
                    underpass.ai/test-id: "24"
                spec:
                  containers:
                    - name: app
                      image: {self.fixture_image}
                      imagePullPolicy: IfNotPresent
                      command:
                        - python
                        - -c
                        - |
                            import http.server
                            import socketserver

                            class Handler(http.server.BaseHTTPRequestHandler):
                                def do_POST(self):
                                    length = int(self.headers.get("Content-Length", "0"))
                                    if length:
                                        self.rfile.read(length)
                                    self.send_response(200)
                                    self.end_headers()
                                    self.wfile.write(b"ok")

                                def log_message(self, fmt, *args):
                                    return

                            socketserver.TCPServer.allow_reuse_address = True
                            with socketserver.TCPServer(("", 8080), Handler) as httpd:
                                httpd.serve_forever()
                      ports:
                        - containerPort: 8080
            ---
            apiVersion: v1
            kind: Service
            metadata:
              name: {self.notify_sink_service}
              namespace: {self.namespace}
              labels:
                app: {self.notify_sink_service}
                underpass.ai/test-id: "24"
            spec:
              selector:
                app: {self.notify_sink_service}
              ports:
                - name: http
                  port: 8080
                  targetPort: 8080
            """
        ).strip()

    def _create_session(self, principal: dict[str, object], metadata: dict[str, str]) -> str:
        return self.create_session(payload={"principal": principal, "metadata": metadata})

    def _invoke_ok(
        self,
        *,
        session_id: str,
        tool_name: str,
        args: dict[str, object],
        approved: bool = False,
        timeout: int = 240,
        label: str,
    ) -> dict[str, object]:
        status, body, invocation = self.invoke(
            session_id=session_id,
            tool_name=tool_name,
            args=args,
            approved=approved,
            timeout=timeout,
        )
        if status != 200:
            raise RuntimeError(f"{label}: expected HTTP 200, got {status}")
        self.assert_invocation_succeeded(invocation=invocation, body=body, label=label)
        return invocation or {}

    def _required_saturation_tools_present(self, session_id: str) -> None:
        status, body = self.request("GET", f"/v1/sessions/{session_id}/tools")
        if status != 200:
            raise RuntimeError(f"list tools: expected 200, got {status}")
        tool_names = {tool.get("name", "") for tool in body.get("tools", [])}
        required = {
            "k8s.apply_manifest",
            "k8s.rollout_status",
            "k8s.get_deployments",
            "k8s.get_pods",
            "k8s.scale_deployment",
            "k8s.restart_pods",
            "k8s.circuit_break",
        }
        missing = sorted(required - tool_names)
        if missing:
            raise RuntimeError(
                "runtime saturation E2E prerequisites not met; missing tools: "
                + ", ".join(missing)
                + " (deploy runtime with WORKSPACE_BACKEND=kubernetes and delivery tools enabled)"
            )

    def _required_notify_tool_present(self, session_id: str) -> None:
        status, body = self.request("GET", f"/v1/sessions/{session_id}/tools")
        if status != 200:
            raise RuntimeError(f"list tools: expected 200, got {status}")
        tool_names = {tool.get("name", "") for tool in body.get("tools", [])}
        if "notify.escalation_channel" not in tool_names:
            raise RuntimeError("notify.escalation_channel not visible in runtime catalog")

    def _wait_rollout(self, session_id: str, deployment_name: str) -> None:
        self._invoke_ok(
            session_id=session_id,
            tool_name="k8s.rollout_status",
            args={
                "namespace": self.namespace,
                "deployment_name": deployment_name,
                "timeout_seconds": 240,
                "poll_interval_ms": 1000,
            },
            approved=True,
            timeout=300,
            label=f"rollout_status_{deployment_name}",
        )

    def _get_single_deployment(self, session_id: str, deployment_name: str) -> dict[str, object]:
        invocation = self._invoke_ok(
            session_id=session_id,
            tool_name="k8s.get_deployments",
            args={
                "namespace": self.namespace,
                "label_selector": f"app={deployment_name}",
                "include_containers": True,
            },
            timeout=120,
            label=f"get_deployments_{deployment_name}",
        )
        output = invocation.get("output", {})
        if not isinstance(output, dict):
            raise RuntimeError(f"get_deployments_{deployment_name}: expected dict output, got {output!r}")
        deployments = output.get("deployments", [])
        if not isinstance(deployments, list) or len(deployments) != 1:
            raise RuntimeError(f"expected one deployment for {deployment_name}, got {deployments!r}")
        deployment = deployments[0]
        if not isinstance(deployment, dict):
            raise RuntimeError(f"expected deployment dict, got {deployment!r}")
        return deployment

    def _wait_for_ready_pods(self, session_id: str, label_selector: str, expected_count: int, timeout_seconds: int = 90) -> None:
        deadline = time.time() + timeout_seconds
        while time.time() < deadline:
            invocation = self._invoke_ok(
                session_id=session_id,
                tool_name="k8s.get_pods",
                args={
                    "namespace": self.namespace,
                    "label_selector": label_selector,
                },
                timeout=120,
                label="get_pods_after_restart",
            )
            output = invocation.get("output", {})
            if isinstance(output, dict):
                pods = output.get("pods", [])
                if isinstance(pods, list):
                    ready = 0
                    for pod in pods:
                        if isinstance(pod, dict) and pod.get("phase") == "Running":
                            ready += int(pod.get("ready_containers", 0) == pod.get("total_containers", 0))
                    if len(pods) >= expected_count and ready >= expected_count:
                        return
            time.sleep(2)
        raise RuntimeError(f"pods with selector {label_selector!r} did not recover to {expected_count} ready pods")

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            saturation_sid = self._create_session(SATURATION_PRINCIPAL, self.saturation_metadata)
            notify_sid = self._create_session(NOTIFY_PRINCIPAL, self.notify_metadata)

            print_step(1, "Runtime exposes saturation specialist tools")
            self._required_saturation_tools_present(saturation_sid)
            self.record_step(
                "required_saturation_tools_present",
                "passed",
                {"namespace": self.namespace, "target_name": self.target_name},
            )
            print_success("Saturation tools are visible for the Kubernetes runtime session")

            print_step(2, "Runtime exposes notify specialist tool")
            self._required_notify_tool_present(notify_sid)
            self.record_step("required_notify_tool_present", "passed")
            print_success("Notify tool is visible for the human-escalation session")

            print_step(3, "Missing saturation tool_profile is denied before execution")
            denied_sat_sid = self.create_session(payload={"principal": SATURATION_PRINCIPAL})
            status, body, invocation = self.invoke(
                session_id=denied_sat_sid,
                tool_name="k8s.scale_deployment",
                args={"namespace": self.namespace, "deployment_name": self.target_name, "replicas": 2},
                approved=True,
                timeout=60,
            )
            if status != 200 or invocation is None or invocation.get("status") != "denied":
                raise RuntimeError(f"expected denied invocation, got status={status}, invocation={invocation}")
            error = self.extract_error(invocation, body)
            if error.get("code") != "policy_denied":
                raise RuntimeError(f"expected policy_denied, got {error}")
            self.record_step("saturation_tool_profile_required", "passed")
            print_success("Missing saturation-operator-bounded profile is denied")

            print_step(4, "Apply fixture deployments, services, and notify sink")
            self._invoke_ok(
                session_id=saturation_sid,
                tool_name="k8s.apply_manifest",
                args={"namespace": self.namespace, "manifest": self._fixture_manifest()},
                approved=True,
                timeout=240,
                label="apply_fixture_manifest",
            )
            for deployment_name in (self.target_name, self.downstream_name, self.notify_sink_deployment):
                self._wait_rollout(saturation_sid, deployment_name)
            self.record_step(
                "fixtures_ready",
                "passed",
                {
                    "target_service": self.target_name,
                    "downstream_service": self.downstream_name,
                    "notify_sink_service": self.notify_sink_service,
                },
            )
            print_success("Fixture deployments and services are ready")

            print_step(5, "Scale deployment succeeds and updates replica count")
            scale_invocation = self._invoke_ok(
                session_id=saturation_sid,
                tool_name="k8s.scale_deployment",
                args={"namespace": self.namespace, "deployment_name": self.target_name, "replicas": 2},
                approved=True,
                timeout=180,
                label="scale_deployment_absolute",
            )
            scale_output = scale_invocation.get("output", {})
            if not isinstance(scale_output, dict):
                raise RuntimeError(f"expected scale output dict, got {scale_output!r}")
            if int(scale_output.get("target_replicas", 0)) != 2:
                raise RuntimeError(f"expected target_replicas=2, got {scale_output!r}")
            self._wait_rollout(saturation_sid, self.target_name)
            deployment = self._get_single_deployment(saturation_sid, self.target_name)
            if int(deployment.get("replicas", 0)) != 2:
                raise RuntimeError(f"expected deployment replicas=2, got {deployment!r}")
            self.record_step("scale_deployment_succeeded", "passed", {"target_replicas": 2})
            print_success("Scale deployment updated the target to 2 replicas")

            print_step(6, "Restart pods without mode is rejected")
            status, body, invocation = self.invoke(
                session_id=saturation_sid,
                tool_name="k8s.restart_pods",
                args={
                    "namespace": self.namespace,
                    "deployment_name": self.target_name,
                },
                approved=True,
                timeout=180,
            )
            if status != 200 or invocation is None or invocation.get("status") != "failed":
                raise RuntimeError(f"expected failed restart_pods invocation, got status={status}, invocation={invocation}")
            error = self.extract_error(invocation, body)
            if error.get("code") != "invalid_argument" or error.get("message") != "mode is required":
                raise RuntimeError(f"expected invalid_argument/mode is required, got {error}")
            self.record_step("restart_pods_mode_required", "passed")
            print_success("Restart pods without mode failed with invalid_argument as expected")

            print_step(7, "Restart pods in label-selector mode deletes only bounded replicas")
            restart_invocation = self._invoke_ok(
                session_id=saturation_sid,
                tool_name="k8s.restart_pods",
                args={
                    "namespace": self.namespace,
                    "deployment_name": self.target_name,
                    "mode": "label_selector",
                    "label_selector": "",
                    "max_pods": 1,
                },
                approved=True,
                timeout=180,
                label="restart_pods_label_selector",
            )
            restart_output = restart_invocation.get("output", {})
            if not isinstance(restart_output, dict):
                raise RuntimeError(f"expected restart output dict, got {restart_output!r}")
            if int(restart_output.get("pods_affected", 0)) != 1:
                raise RuntimeError(f"expected pods_affected=1, got {restart_output!r}")
            deleted_pods = restart_output.get("deleted_pods", [])
            if not isinstance(deleted_pods, list) or len(deleted_pods) != 1:
                raise RuntimeError(f"expected exactly one deleted pod, got {restart_output!r}")
            self._wait_for_ready_pods(saturation_sid, f"app={self.target_name}", expected_count=2)
            self.record_step("restart_pods_succeeded", "passed", {"deleted_pod": deleted_pods[0]})
            print_success("Restart pods deleted one bounded replica and the deployment recovered")

            print_step(8, "Circuit break installs a bounded network policy")
            circuit_invocation = self._invoke_ok(
                session_id=saturation_sid,
                tool_name="k8s.circuit_break",
                args={
                    "namespace": self.namespace,
                    "target_service": self.target_name,
                    "downstream": self.downstream_name,
                    "ttl_seconds": 120,
                },
                approved=True,
                timeout=180,
                label="circuit_break_success",
            )
            circuit_output = circuit_invocation.get("output", {})
            if not isinstance(circuit_output, dict):
                raise RuntimeError(f"expected circuit break output dict, got {circuit_output!r}")
            if circuit_output.get("mesh_kind") != "networkpolicy":
                raise RuntimeError(f"expected networkpolicy mesh kind, got {circuit_output!r}")
            if not str(circuit_output.get("policy_id", "")).strip():
                raise RuntimeError(f"expected non-empty policy_id, got {circuit_output!r}")
            self.record_step(
                "circuit_break_succeeded",
                "passed",
                {"policy_id": circuit_output.get("policy_id"), "expires_at": circuit_output.get("expires_at")},
            )
            print_success("Circuit break installed a NetworkPolicy-backed block")

            print_step(9, "Notify escalation succeeds for the e2e route")
            incident_id = f"e2e-incident-{self.run_id}"
            status, body, invocation = self.invoke(
                session_id=notify_sid,
                tool_name="notify.escalation_channel",
                args={
                    "incident_id": incident_id,
                    "handoff_node_id": f"handoff-{self.run_id}",
                    "summary": "CPU saturation detected on payments-api",
                    "upstream_specialist": "human-escalation",
                    "upstream_decision": "engage_owner",
                    "reason": "bounded saturation actions applied",
                    "resource_ref": f"deployment/{self.target_name}",
                },
                approved=True,
                timeout=120,
            )
            if status != 200 or invocation is None:
                raise RuntimeError(f"notify invoke failed unexpectedly: status={status}, invocation={invocation}")
            if invocation.get("status") != "succeeded":
                error = self.extract_error(invocation, body)
                if error.get("code") == "execution_failed" and "route" in str(error.get("message", "")):
                    raise RuntimeError(
                        "notify route not configured for environment e2e; set "
                        "WORKSPACE_NOTIFY_ESCALATION_ROUTES_JSON to point at "
                        f"http://{self.notify_sink_service}.{self.namespace}.svc.cluster.local:8080/notify"
                    )
                raise RuntimeError(f"notify success path failed: {error}")
            notify_output = invocation.get("output", {})
            if not isinstance(notify_output, dict):
                raise RuntimeError(f"expected notify output dict, got {notify_output!r}")
            if not notify_output.get("delivered"):
                raise RuntimeError(f"expected delivered=true, got {notify_output!r}")
            self.record_step(
                "notify_escalation_succeeded",
                "passed",
                {"channel": notify_output.get("channel"), "provider": notify_output.get("provider")},
            )
            print_success("Notify escalation delivered through the configured e2e route")

            print_step(10, "Second notify within one minute is rate-limited")
            status, body, invocation = self.invoke(
                session_id=notify_sid,
                tool_name="notify.escalation_channel",
                args={
                    "incident_id": incident_id,
                    "handoff_node_id": f"handoff-{self.run_id}",
                    "summary": "CPU saturation detected on payments-api",
                    "upstream_specialist": "human-escalation",
                    "upstream_decision": "engage_owner",
                    "reason": "bounded saturation actions applied",
                    "resource_ref": f"deployment/{self.target_name}",
                },
                approved=True,
                timeout=120,
            )
            if status != 200 or invocation is None or invocation.get("status") != "denied":
                raise RuntimeError(f"expected denied notify invocation, got status={status}, invocation={invocation}")
            error = self.extract_error(invocation, body)
            if error.get("code") != "policy_denied" or error.get("message") != "rate_limit_exceeded":
                raise RuntimeError(f"expected rate_limit_exceeded denial, got {error}")
            self.record_step("notify_rate_limit_enforced", "passed")
            print_success("Notify rate limit denied the duplicate incident escalation")

            final_status = "passed"
            print_success("Runtime saturation + notify tools E2E passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return RuntimeSaturationNotifyToolsE2E().run()


if __name__ == "__main__":
    sys.exit(main())
