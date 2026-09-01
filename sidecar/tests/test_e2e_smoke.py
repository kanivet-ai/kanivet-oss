import asyncio
import json
import os
import subprocess
import tempfile
import threading
import time
import uuid
from pathlib import Path

import pytest
import websockets


SIDECAR_BINARY = Path(__file__).parent.parent.parent / "frontend" / "resources" / "kanivetai-sidecar" / "kanivetai-sidecar"


def _serve_fake_mcp(read_fd: int, write_fd: int) -> None:
    """Respond to MCP initialize + tools/list so the sidecar can start up."""
    try:
        with os.fdopen(read_fd, "rb", buffering=0) as r, os.fdopen(write_fd, "wb", buffering=0) as w:
            buf = b""
            while True:
                chunk = r.read(4096)
                if not chunk:
                    break
                buf += chunk
                while b"\n" in buf:
                    line, buf = buf.split(b"\n", 1)
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        msg = json.loads(line)
                    except Exception:
                        continue
                    method = msg.get("method", "")
                    msg_id = msg.get("id")
                    if method == "initialize":
                        resp = {
                            "jsonrpc": "2.0",
                            "id": msg_id,
                            "result": {
                                "protocolVersion": "2024-11-05",
                                "capabilities": {"tools": {}},
                                "serverInfo": {"name": "fake-kanivet", "version": "0.0.0"},
                            },
                        }
                        w.write((json.dumps(resp) + "\n").encode())
                        w.flush()
                    elif method == "notifications/initialized":
                        pass
                    elif method == "tools/list":
                        resp = {
                            "jsonrpc": "2.0",
                            "id": msg_id,
                            "result": {"tools": []},
                        }
                        w.write((json.dumps(resp) + "\n").encode())
                        w.flush()
    except Exception:
        pass


@pytest.mark.asyncio
@pytest.mark.skipif(not SIDECAR_BINARY.exists(), reason=f"sidecar binary not built at {SIDECAR_BINARY}")
async def test_sidecar_binary_round_trips_a_chat_message() -> None:
    tmp = tempfile.mkdtemp(prefix="kvai-e2e-", dir="/tmp")
    sock = os.path.join(tmp, "s.sock")
    token = str(uuid.uuid4())

    # Pipe pair: sidecar reads from r_in, test writes to w_in (fake MCP server → sidecar)
    r_in, w_in = os.pipe()
    # Pipe pair: sidecar writes to w_out, test reads from r_out (sidecar → fake MCP server)
    r_out, w_out = os.pipe()

    proc = subprocess.Popen(
        [
            str(SIDECAR_BINARY),
            "--ws-socket", sock,
            "--auth-token", token,
            "--mcp-stdio-fd-in", str(r_in),
            "--mcp-stdio-fd-out", str(w_out),
        ],
        pass_fds=(r_in, w_out),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    os.close(r_in)
    os.close(w_out)

    mcp_thread = threading.Thread(target=_serve_fake_mcp, args=(r_out, w_in), daemon=True)
    mcp_thread.start()

    try:
        deadline = time.monotonic() + 30.0
        while time.monotonic() < deadline:
            if os.path.exists(sock):
                break
            if proc.poll() is not None:
                stdout = proc.stdout.read().decode(errors="replace") if proc.stdout else ""
                stderr = proc.stderr.read().decode(errors="replace") if proc.stderr else ""
                pytest.fail(f"sidecar exited early code={proc.returncode}\nstdout:\n{stdout}\nstderr:\n{stderr}")
            await asyncio.sleep(0.2)
        if not os.path.exists(sock):
            if proc.poll() is not None:
                stdout = proc.stdout.read().decode(errors="replace") if proc.stdout else ""
                stderr = proc.stderr.read().decode(errors="replace") if proc.stderr else ""
                pytest.fail(f"sidecar exited code={proc.returncode}\nstdout:\n{stdout}\nstderr:\n{stderr}")
            pytest.fail("sidecar did not create socket within 30s")

        async with websockets.unix_connect(sock, additional_headers={"X-Auth-Token": token}) as ws:
            await ws.send(json.dumps({
                "type": "agent.chat",
                "sessionId": "sm1",
                "cluster": "minikube",
                "message": "hi",
                "resources": [],
                "uiSnapshot": {},
                "llmConfig": {"apiKey": "bogus", "baseURL": "http://127.0.0.1:1", "model": "default"},
            }))
            raw = await asyncio.wait_for(ws.recv(), timeout=90.0)
            evt = json.loads(raw)
            assert evt.get("sessionId") == "sm1", f"unexpected event: {evt}"
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
        mcp_thread.join(timeout=2)
