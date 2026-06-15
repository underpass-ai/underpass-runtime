"""E2E test: runtime rollout specialist tools via Kubernetes Job."""

from __future__ import annotations

import os
import sys
import textwrap
import time
from urllib.parse import urlparse

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"
DEFAULT_NAMESPACE = "underpass-runtime"
IMAGE_V1 = os.getenv("E2E_ROLLOUT_IMAGE_V1", "registry.k8s.io/pause:3.8")
IMAGE_V2 = os.getenv("E2E_ROLLOUT_IMAGE_V2", "registry.k8s.io/pause:3.9")
WAIT_FOR_SAFE_UNDO_SECONDS = int(os.getenv("E2E_ROLLOUT_SAFE_WAIT_SECONDS", "65"))

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-rollout-actor",
    "roles": ["devops", "platform_admin"],
}


class RuntimeRolloutToolsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="23-runtime-rollout-tools",
            run_id_prefix="e2e-rollout",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-23-{int(time.time())}.json"),
        )
        self.namespace = os.getenv(
            "K8S_TARGET_NAMESPACE",
            os.getenv("WORKSPACE_NAMESPACE", self._infer_namespace()),
        )
        suffix = self.run_id.rsplit("-", 1)[-1]
        self.deployment_name = f"e2e-rollout-{suffix}"
        self.session_metadata = {
            "tool_profile": "runtime-rollout-narrow",
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

    def _manifest(self, image: str) -> str:
        return textwrap.dedent(
            f"""\
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: {self.deployment_name}
              namespace: {self.namespace}
              labels:
                app: {self.deployment_name}
                underpass.ai/test-id: "23"
                underpass.ai/run-id: "{self.run_id}"
            spec:
              replicas: 1
              revisionHistoryLimit: 3
              selector:
                matchLabels:
                  app: {self.deployment_name}
              template:
                metadata:
                  labels:
                    app: {self.deployment_name}
                    underpass.ai/test-id: "23"
                    underpass.ai/run-id: "{self.run_id}"
                spec:
                  terminationGracePeriodSeconds: 0
                  containers:
                    - name: app
                      image: {image}
                      imagePullPolicy: IfNotPresent
            """
        ).strip()

    def _create_rollout_session(self) -> str:
        return self.create_session(
            payload={
                "principal": PRINCIPAL,
                "metadata": self.session_metadata,
            }
        )

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

    def _required_tools_present(self, session_id: str) -> None:
        status, body = self.request("GET", f"/v1/sessions/{session_id}/tools")
        if status != 200:
            raise RuntimeError(f"list tools: expected 200, got {status}")
        tools = body.get("tools", [])
        tool_names = {tool.get("name", "") for tool in tools}
        required = {
            "k8s.apply_manifest",
            "k8s.rollout_status",
            "k8s.get_replicasets",
            "k8s.rollout_pause",
            "k8s.rollout_undo",
        }
        missing = sorted(required - tool_names)
        if missing:
            raise RuntimeError(
                "runtime rollout E2E prerequisites not met; missing tools: "
                + ", ".join(missing)
                + " (deploy runtime with WORKSPACE_BACKEND=kubernetes and delivery tools enabled)"
            )

    @staticmethod
    def _current_replicaset(output: dict[str, object]) -> dict[str, object]:
        replicasets = output.get("replicasets", [])
        if not isinstance(replicasets, list) or not replicasets:
            raise RuntimeError(f"expected non-empty replicaset list, got {replicasets!r}")
        current = [rs for rs in replicasets if isinstance(rs, dict) and rs.get("current")]
        if len(current) != 1:
            raise RuntimeError(f"expected exactly one current replicaset, got {current!r}")
        return current[0]

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            rollout_sid = self._create_rollout_session()

            print_step(1, "Runtime exposes rollout specialist tools")
            self._required_tools_present(rollout_sid)
            self.record_step(
                "required_tools_present",
                "passed",
                {"deployment_name": self.deployment_name, "namespace": self.namespace},
            )
            print_success("Rollout tools are visible for the Kubernetes runtime session")

            print_step(2, "Missing tool_profile is denied before execution")
            denied_sid = self.create_session(payload={"principal": PRINCIPAL})
            status, body, invocation = self.invoke(
                session_id=denied_sid,
                tool_name="k8s.get_replicasets",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                timeout=60,
            )
            if status != 200 or invocation is None or invocation.get("status") != "denied":
                raise RuntimeError(f"expected denied invocation, got status={status}, invocation={invocation}")
            error = self.extract_error(invocation, body)
            if error.get("code") != "policy_denied":
                raise RuntimeError(f"expected policy_denied, got {error}")
            self.record_step("tool_profile_required", "passed")
            print_success("Missing runtime-rollout-narrow profile is denied")

            print_step(3, "Apply revision A and wait for rollout")
            self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.apply_manifest",
                args={"namespace": self.namespace, "manifest": self._manifest(IMAGE_V1)},
                approved=True,
                timeout=180,
                label="apply_manifest_revision_a",
            )
            self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.rollout_status",
                args={
                    "namespace": self.namespace,
                    "deployment_name": self.deployment_name,
                    "timeout_seconds": 240,
                    "poll_interval_ms": 1000,
                },
                approved=True,
                timeout=300,
                label="rollout_status_revision_a",
            )
            self.record_step("revision_a_ready", "passed", {"image": IMAGE_V1})
            print_success("Revision A is ready")

            print_step(4, "ReplicaSet listing returns the current healthy revision")
            get_rs_a = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.get_replicasets",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                timeout=120,
                label="get_replicasets_revision_a",
            )
            rs_output_a = get_rs_a.get("output", {})
            current_a = self._current_replicaset(rs_output_a if isinstance(rs_output_a, dict) else {})
            original_revision = int(current_a.get("revision", 0))
            if original_revision < 1:
                raise RuntimeError(f"expected revision >= 1, got {current_a}")
            if int(current_a.get("ready_replicas", 0)) < 1:
                raise RuntimeError(f"expected ready current replicaset, got {current_a}")
            self.record_step("replicasets_revision_a", "passed", {"revision": original_revision})
            print_success(f"Current ReplicaSet revision is {original_revision}")

            print_step(5, "Apply revision B and wait for rollout")
            self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.apply_manifest",
                args={"namespace": self.namespace, "manifest": self._manifest(IMAGE_V2)},
                approved=True,
                timeout=180,
                label="apply_manifest_revision_b",
            )
            self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.rollout_status",
                args={
                    "namespace": self.namespace,
                    "deployment_name": self.deployment_name,
                    "timeout_seconds": 240,
                    "poll_interval_ms": 1000,
                },
                approved=True,
                timeout=300,
                label="rollout_status_revision_b",
            )
            get_rs_b = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.get_replicasets",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                timeout=120,
                label="get_replicasets_revision_b",
            )
            rs_output_b = get_rs_b.get("output", {})
            current_b = self._current_replicaset(rs_output_b if isinstance(rs_output_b, dict) else {})
            new_revision = int(current_b.get("revision", 0))
            if new_revision == original_revision:
                raise RuntimeError(
                    f"expected a new revision after image update, got original={original_revision}, current={current_b}"
                )
            replicasets_b = rs_output_b.get("replicasets", []) if isinstance(rs_output_b, dict) else []
            if len(replicasets_b) < 2:
                raise RuntimeError(f"expected at least 2 replicasets after rollout, got {replicasets_b}")
            self.record_step("revision_b_ready", "passed", {"revision": new_revision, "image": IMAGE_V2})
            print_success(f"Revision B is ready as revision {new_revision}")

            print_step(6, "Undo is denied while the new rollout is still too young")
            status, body, invocation = self.invoke(
                session_id=rollout_sid,
                tool_name="k8s.rollout_undo",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                approved=True,
                timeout=120,
            )
            if status != 200 or invocation is None or invocation.get("status") != "denied":
                raise RuntimeError(f"expected denied undo invocation, got status={status}, invocation={invocation}")
            error = self.extract_error(invocation, body)
            if error.get("code") != "rollout_too_young":
                raise RuntimeError(f"expected rollout_too_young, got {error}")
            self.record_step("undo_too_young", "passed")
            print_success("Young rollout preflight denied the undo")

            print_step(7, "Wait for the rollback safety window to expire")
            time.sleep(WAIT_FOR_SAFE_UNDO_SECONDS)
            self.record_step("safe_undo_wait_elapsed", "passed", {"seconds": WAIT_FOR_SAFE_UNDO_SECONDS})
            print_success(f"Waited {WAIT_FOR_SAFE_UNDO_SECONDS}s for safe rollback window")

            print_step(8, "Undo rolls back to the previous healthy ReplicaSet")
            undo_invocation = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.rollout_undo",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                approved=True,
                timeout=180,
                label="rollout_undo_success",
            )
            undo_output = undo_invocation.get("output", {})
            if not isinstance(undo_output, dict):
                raise RuntimeError(f"expected dict undo output, got {undo_output!r}")
            if not undo_output.get("rolled_back"):
                raise RuntimeError(f"expected rolled_back=true, got {undo_output}")
            if int(undo_output.get("target_revision", 0)) != original_revision:
                raise RuntimeError(
                    f"expected target revision {original_revision}, got {undo_output.get('target_revision')}"
                )
            self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.rollout_status",
                args={
                    "namespace": self.namespace,
                    "deployment_name": self.deployment_name,
                    "timeout_seconds": 240,
                    "poll_interval_ms": 1000,
                },
                approved=True,
                timeout=300,
                label="rollout_status_after_undo",
            )
            self.record_step("undo_succeeded", "passed", {"target_revision": original_revision})
            print_success(f"Rollback completed to revision {original_revision}")

            print_step(9, "Deployment template rolls back even though Kubernetes creates a new revision")
            get_rs_after_undo = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.get_replicasets",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                timeout=120,
                label="get_replicasets_after_undo",
            )
            rs_output_after_undo = get_rs_after_undo.get("output", {})
            current_after_undo = self._current_replicaset(
                rs_output_after_undo if isinstance(rs_output_after_undo, dict) else {}
            )
            current_after_undo_revision = int(current_after_undo.get("revision", 0))
            if current_after_undo_revision == new_revision:
                raise RuntimeError(
                    f"expected undo to move away from revision {new_revision}, got {current_after_undo}"
                )
            get_deployments = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.get_deployments",
                args={
                    "namespace": self.namespace,
                    "label_selector": f"app={self.deployment_name}",
                    "include_containers": True,
                },
                timeout=120,
                label="get_deployments_after_undo",
            )
            deployments_output = get_deployments.get("output", {})
            deployments = deployments_output.get("deployments", []) if isinstance(deployments_output, dict) else []
            if len(deployments) != 1:
                raise RuntimeError(f"expected one deployment after undo, got {deployments!r}")
            containers = deployments[0].get("containers", [])
            if not isinstance(containers, list) or len(containers) != 1:
                raise RuntimeError(f"expected one deployment container after undo, got {containers!r}")
            container_image = containers[0].get("image")
            if container_image != IMAGE_V1:
                raise RuntimeError(f"expected deployment image {IMAGE_V1} after undo, got {container_image!r}")
            self.record_step(
                "replicasets_after_undo",
                "passed",
                {"current_revision": current_after_undo_revision, "deployment_image": container_image},
            )
            print_success("Deployment template rolled back to revision A semantics")

            print_step(10, "Rollout pause succeeds on the deployment")
            pause_invocation = self._invoke_ok(
                session_id=rollout_sid,
                tool_name="k8s.rollout_pause",
                args={"namespace": self.namespace, "deployment_name": self.deployment_name},
                approved=True,
                timeout=120,
                label="rollout_pause_success",
            )
            pause_output = pause_invocation.get("output", {})
            if not isinstance(pause_output, dict) or not pause_output.get("paused"):
                raise RuntimeError(f"expected paused=true, got {pause_output!r}")
            self.record_step("pause_succeeded", "passed")
            print_success("Deployment pause succeeded")

            final_status = "passed"
            print_success("Runtime rollout tools E2E passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return RuntimeRolloutToolsE2E().run()


if __name__ == "__main__":
    sys.exit(main())
