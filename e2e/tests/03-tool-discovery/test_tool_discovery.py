"""E2E test: Tool discovery — list tools, compact/full detail, filters."""

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


class ToolDiscoveryE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="03-tool-discovery",
            run_id_prefix="e2e-discovery",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-03-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # --- Step 1: List all tools ---
            print_step(1, "GET /v1/sessions/{sid}/tools returns tool list")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools")
            if status != 200:
                raise RuntimeError(f"list tools: expected 200, got {status}")
            tools = body.get("tools", [])
            if len(tools) < 1:
                raise RuntimeError(f"expected at least 1 tool, got {len(tools)}")
            tool_names = [t.get("name", "") for t in tools]
            for expected in ("fs.list", "fs.read_file", "git.status"):
                if expected not in tool_names:
                    raise RuntimeError(f"expected tool '{expected}' in catalog, got {tool_names[:10]}")
            self.record_step("list_tools", "passed", {"count": len(tools)})
            print_success(f"Listed {len(tools)} tools, known tools present")

            # --- Step 2: Invalid session returns 404 ---
            print_step(2, "List tools with invalid session returns 404")
            status, _ = self.request("GET", "/v1/sessions/does-not-exist/tools")
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("list_invalid_session", "passed")
            print_success("Invalid session -> 404")

            # --- Step 3: Discovery compact (default) ---
            print_step(3, "Discovery compact default")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery")
            if status != 200:
                raise RuntimeError(f"discovery compact: expected 200, got {status}")
            total = body.get("total", 0)
            filtered = body.get("filtered", 0)
            if total < 1 or filtered < 1:
                raise RuntimeError(f"expected total>0 and filtered>0, got total={total} filtered={filtered}")
            if filtered > total:
                raise RuntimeError(f"filtered ({filtered}) > total ({total})")
            disc_tools = body.get("tools", [])
            for t in disc_tools[:3]:
                if "name" not in t or "description" not in t:
                    raise RuntimeError(f"compact tool missing name/description: {t}")
            self.record_step("discovery_compact", "passed", {"total": total, "filtered": filtered})
            print_success(f"Discovery compact: total={total}, filtered={filtered}")

            # --- Step 4: Discovery full detail ---
            print_step(4, "Discovery full detail includes input_schema")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery?detail=full")
            if status != 200:
                raise RuntimeError(f"discovery full: expected 200, got {status}")
            full_tools = body.get("tools", [])
            # FullTool wraps Tool at field "tool", so name is at t["tool"]["name"]
            def tool_name(t: dict) -> str:
                return t.get("name", "") or t.get("tool", {}).get("name", "")
            fs_list = next((t for t in full_tools if tool_name(t) == "fs.list"), None)
            if fs_list is None:
                raise RuntimeError(f"fs.list not found in full discovery ({len(full_tools)} tools)")
            # input_schema may be nested under tool.inputSchema (protobuf camelCase)
            inner = fs_list.get("tool", fs_list)
            if "input_schema" not in inner and "inputSchema" not in inner:
                raise RuntimeError(f"fs.list missing input_schema in full detail: {list(inner.keys())}")
            self.record_step("discovery_full", "passed")
            print_success("Full detail includes input_schema")

            # --- Step 5: Filter by risk=low ---
            print_step(5, "Filter by risk=low")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery?risk=low")
            if status != 200:
                raise RuntimeError(f"filter risk: expected 200, got {status}")
            for t in body.get("tools", []):
                risk = t.get("risk", "")
                if risk != "low":
                    raise RuntimeError(f"filter risk=low violated: tool {t.get('name')} has risk={risk}")
            self.record_step("filter_risk", "passed", {"filtered": body.get("filtered", 0)})
            print_success(f"Risk=low filter: {body.get('filtered', 0)} tools")

            # --- Step 6: Filter by cost ---
            print_step(6, "Filter by cost=cheap")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery?cost=cheap")
            if status != 200:
                raise RuntimeError(f"filter cost: expected 200, got {status}")
            for t in body.get("tools", []):
                # cost field may be named differently; check common variants
                cost = t.get("cost", t.get("cost_hint", ""))
                if cost and cost not in ("cheap", "low"):
                    raise RuntimeError(f"filter cost=cheap violated: tool {t.get('name')} has cost={cost}")
            self.record_step("filter_cost", "passed", {"filtered": body.get("filtered", 0)})
            print_success(f"Cost filter: {body.get('filtered', 0)} tools")

            # --- Step 7: Filter by side_effects=none ---
            print_step(7, "Filter by side_effects=none")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery?side_effects=none")
            if status != 200:
                raise RuntimeError(f"filter side_effects: expected 200, got {status}")
            for t in body.get("tools", []):
                se = t.get("side_effects", "")
                if se and se != "none":
                    raise RuntimeError(f"filter side_effects=none violated: {t.get('name')} has {se}")
            self.record_step("filter_side_effects", "passed", {"filtered": body.get("filtered", 0)})
            print_success(f"Side effects filter: {body.get('filtered', 0)} tools")

            # --- Step 8: Filter by scope=repo ---
            print_step(8, "Filter by scope=repo")
            status, body = self.request("GET", f"/v1/sessions/{sid}/tools/discovery?scope=repo")
            if status != 200:
                raise RuntimeError(f"filter scope: expected 200, got {status}")
            if body.get("filtered", 0) < 1:
                raise RuntimeError("expected at least 1 repo-scoped tool")
            self.record_step("filter_scope", "passed", {"filtered": body.get("filtered", 0)})
            print_success(f"Scope filter: {body.get('filtered', 0)} tools")

            # --- Step 9: Combined filters ---
            print_step(9, "Combined filters: risk=low AND side_effects=none")
            status, body = self.request(
                "GET", f"/v1/sessions/{sid}/tools/discovery?risk=low&side_effects=none"
            )
            if status != 200:
                raise RuntimeError(f"combined filters: expected 200, got {status}")
            for t in body.get("tools", []):
                risk = t.get("risk", "")
                se = t.get("side_effects", "")
                if (risk and risk != "low") or (se and se != "none"):
                    raise RuntimeError(f"combined filter violated: {t.get('name')} risk={risk} se={se}")
            self.record_step("combined_filters", "passed", {"filtered": body.get("filtered", 0)})
            print_success(f"Combined filters: {body.get('filtered', 0)} tools")

            # --- Step 10: Discovery with invalid session returns 404 ---
            print_step(10, "Discovery with invalid session returns 404")
            status, _ = self.request("GET", "/v1/sessions/does-not-exist/tools/discovery")
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("discovery_invalid_session", "passed")
            print_success("Discovery invalid session -> 404")

            final_status = "passed"
            print_success("All tool discovery tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return ToolDiscoveryE2E().run()


if __name__ == "__main__":
    sys.exit(main())
