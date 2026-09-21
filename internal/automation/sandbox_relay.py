#!/usr/bin/env python3
"""In-sandbox TCP-to-UNIX relay for allowlisted model-API CONNECT access.

The host proxy enforces the destination allowlist. This process only exposes
that proxy on loopback so a CLI can use HTTP(S)_PROXY while the Bubblewrap
network namespace has no routed interfaces for tool/project traffic.
"""
import os
import socket
import subprocess
import sys
import threading

SOCK = os.environ.get("SHIPYARD_MODEL_API_SOCKET", "/tmp/shipyard-model-api.sock")
LISTEN_HOST = "127.0.0.1"
LISTEN_PORT = int(os.environ.get("SHIPYARD_MODEL_API_PORT", "18765"))


def _forward(src, dst):
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except OSError:
        pass
    for side in (src, dst):
        try:
            side.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        try:
            side.close()
        except OSError:
            pass


def _handle(client):
    upstream = socket.socket(socket.AF_UNIX)
    try:
        upstream.connect(SOCK)
    except OSError:
        try:
            client.close()
        except OSError:
            pass
        return
    threading.Thread(target=_forward, args=(client, upstream), daemon=True).start()
    _forward(upstream, client)


def _serve():
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((LISTEN_HOST, LISTEN_PORT))
    server.listen(64)
    while True:
        client, _ = server.accept()
        threading.Thread(target=_handle, args=(client,), daemon=True).start()


def main():
    if len(sys.argv) < 2:
        sys.stderr.write("shipyard model-api relay: missing command\n")
        sys.exit(2)
    threading.Thread(target=_serve, daemon=True).start()
    proxy = "http://%s:%d" % (LISTEN_HOST, LISTEN_PORT)
    os.environ["HTTP_PROXY"] = proxy
    os.environ["HTTPS_PROXY"] = proxy
    os.environ["ALL_PROXY"] = proxy
    os.environ["http_proxy"] = proxy
    os.environ["https_proxy"] = proxy
    os.environ["all_proxy"] = proxy
    result = subprocess.run(sys.argv[1:])
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()
