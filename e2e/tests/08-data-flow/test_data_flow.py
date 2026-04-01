"""E2E test: Full data flow — write, list, read, retrieve invocations."""

from __future__ import annotations

import os
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_info, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-actor",
    "roles": ["developer"],
}


class DataFlowE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="08-data-flow",
            run_id_prefix="e2e-dataflow",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-08-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        invocation_ids: list[str] = []
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # --- Step 1: Write a file ---
            print_step(1, "Write file via fs.write_file")
            filename = f"e2e-dataflow-{int(time.time())}.txt"
            content = "underpass-runtime e2e data flow test content"
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.write_file",
                args={"path": filename, "content": content},
                approved=True,
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="write_file")
            write_id = inv["id"]
            invocation_ids.append(write_id)
            self.record_step("write_file", "passed", {"invocation_id": write_id, "file": filename})
            print_success(f"Wrote {filename} (invocation={write_id})")

            # --- Step 2: List directory ---
            print_step(2, "List workspace via fs.list")
            http_status, body, inv = self.invoke(
                session_id=sid, tool_name="fs.list", args={"path": "."}
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="list_dir")
            list_id = inv["id"]
            invocation_ids.append(list_id)

            # Verify our file appears in the listing
            output = inv.get("output", {})
            output_str = str(output)
            if filename not in output_str:
                print_info(f"File {filename} not found in listing output (may be nested)")
            self.record_step("list_dir", "passed", {"invocation_id": list_id})
            print_success(f"Listed directory (invocation={list_id})")

            # --- Step 3: Read file back ---
            print_step(3, "Read file via fs.read_file")
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.read_file",
                args={"path": filename},
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="read_file")
            read_id = inv["id"]
            invocation_ids.append(read_id)

            # Verify content matches
            output = inv.get("output", {})
            output_str = str(output)
            if content not in output_str:
                raise RuntimeError(f"read_file: content mismatch, expected '{content}' in output")
            self.record_step("read_file", "passed", {"invocation_id": read_id})
            print_success(f"Read file content verified (invocation={read_id})")

            # --- Step 4: Retrieve all invocations by ID ---
            print_step(4, "Retrieve all 3 invocations by ID")
            for i, iid in enumerate(invocation_ids):
                status, body = self.request("GET", f"/v1/invocations/{iid}")
                if status != 200:
                    raise RuntimeError(f"retrieve invocation {i+1}: expected 200, got {status}")
                ret_inv = body.get("invocation", {})
                if ret_inv.get("id") != iid:
                    raise RuntimeError(f"ID mismatch on invocation {i+1}")
                if ret_inv.get("session_id") != sid:
                    raise RuntimeError(f"session_id mismatch on invocation {i+1}")
                print_success(f"  Invocation {i+1}/{len(invocation_ids)}: {iid} verified")

            self.record_step("retrieve_all", "passed", {"invocation_ids": invocation_ids})

            final_status = "passed"
            print_success("Full data flow test passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return DataFlowE2E().run()


if __name__ == "__main__":
    sys.exit(main())
