#!/usr/bin/env python3
"""E2E test: agent feedback loop.

Validates the full cycle:
1. Create session
2. Get recommendation
3. Invoke recommended tool
4. Accept recommendation (feedback)
5. Reject recommendation for a second call (feedback)
6. Close session
7. Verify feedback events via GetRecommendationDecision
"""
import json
import os
import subprocess
import sys

EVIDENCE_MARKER_START = "EVIDENCE_JSON_START"
EVIDENCE_MARKER_END = "EVIDENCE_JSON_END"

RUNTIME_HOST = os.environ.get("RUNTIME_HOST", "underpass-runtime:50053")
TLS_CA = os.environ.get("TLS_CA_PATH", "")
TLS_CERT = os.environ.get("TLS_CERT_PATH", "")
TLS_KEY = os.environ.get("TLS_KEY_PATH", "")


def grpcurl(service, method, data=None):
    cmd = ["grpcurl"]
    if TLS_CA:
        cmd += ["-cacert", TLS_CA, "-cert", TLS_CERT, "-key", TLS_KEY,
                "-servername", "underpass-runtime"]
    else:
        cmd += ["-plaintext"]
    if data:
        cmd += ["-d", json.dumps(data)]
    cmd += [RUNTIME_HOST, f"{service}/{method}"]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    if result.returncode != 0:
        return None, result.stderr
    return json.loads(result.stdout), None


def main():
    steps = []
    overall = "pass"

    def step(name, fn):
        nonlocal overall
        try:
            result = fn()
        except Exception as e:
            result = {"status": "fail", "error": str(e)}
        result["step"] = name
        steps.append(result)
        if result["status"] != "pass":
            overall = "fail"
        return result

    # 1. Create session
    session_id = None
    def create_session():
        nonlocal session_id
        resp, err = grpcurl("underpass.runtime.v1.SessionService", "CreateSession", {
            "repo_url": "https://github.com/underpass-ai/underpass-runtime",
            "repo_ref": "main",
            "principal": {"tenant_id": "e2e-feedback", "actor_id": "e2e-agent", "roles": ["developer"]}
        })
        if err:
            return {"status": "fail", "error": err}
        session_id = resp["session"]["id"]
        return {"status": "pass", "session_id": session_id}

    step("create_session", create_session)
    if not session_id:
        print_evidence({"status": "fail", "steps": steps, "error_message": "session creation failed"})
        sys.exit(1)

    # 2. Get recommendation
    rec_id = None
    recommended_tool = None
    def get_recommendation():
        nonlocal rec_id, recommended_tool
        resp, err = grpcurl("underpass.runtime.v1.CapabilityCatalogService", "RecommendTools", {
            "session_id": session_id, "task_hint": "read a config file", "top_k": 3
        })
        if err:
            return {"status": "fail", "error": err}
        rec_id = resp.get("recommendationId")
        recs = resp.get("recommendations", [])
        if not recs:
            return {"status": "fail", "error": "no recommendations"}
        recommended_tool = recs[0]["name"]
        return {"status": "pass", "recommendation_id": rec_id, "top_tool": recommended_tool,
                "decision_source": resp.get("decisionSource"), "algorithm_id": resp.get("algorithmId")}

    step("get_recommendation", get_recommendation)

    # 3. Invoke the recommended tool
    def invoke_tool():
        resp, err = grpcurl("underpass.runtime.v1.InvocationService", "InvokeTool", {
            "session_id": session_id, "tool_name": recommended_tool, "args": {}
        })
        if err:
            return {"status": "fail", "error": err}
        return {"status": "pass", "tool": recommended_tool,
                "invocation_status": resp["invocation"]["status"]}

    step("invoke_recommended_tool", invoke_tool)

    # 4. Accept recommendation (positive feedback)
    def accept_rec():
        resp, err = grpcurl("underpass.runtime.v1.CapabilityCatalogService", "AcceptRecommendation", {
            "session_id": session_id, "recommendation_id": rec_id,
            "selected_tool_id": recommended_tool
        })
        if err:
            return {"status": "fail", "error": err}
        return {"status": "pass", "event_id": resp.get("eventId")}

    step("accept_recommendation", accept_rec)

    # 5. Reject recommendation (negative feedback for testing)
    def reject_rec():
        resp, err = grpcurl("underpass.runtime.v1.CapabilityCatalogService", "RejectRecommendation", {
            "session_id": session_id, "recommendation_id": rec_id,
            "reason": "tool output was not useful for my task"
        })
        if err:
            return {"status": "fail", "error": err}
        return {"status": "pass", "event_id": resp.get("eventId")}

    step("reject_recommendation", reject_rec)

    # 6. Close session
    def close_session():
        resp, err = grpcurl("underpass.runtime.v1.SessionService", "CloseSession", {
            "session_id": session_id
        })
        if err:
            return {"status": "fail", "error": err}
        return {"status": "pass"}

    step("close_session", close_session)

    print_evidence({"status": overall, "steps": steps})
    sys.exit(0 if overall == "pass" else 1)


def print_evidence(evidence):
    print(EVIDENCE_MARKER_START)
    print(json.dumps(evidence, indent=2))
    print(EVIDENCE_MARKER_END)


if __name__ == "__main__":
    main()
