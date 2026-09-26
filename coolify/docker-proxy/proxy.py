"""Read-only Docker API proxy for the Beszel agent.

Forwards GET/HEAD to the Docker socket and refuses everything else. On
/containers/json it rewrites each container's display name from its Coolify
labels, so Beszel shows "facemap-web" instead of "dcgdzzlcxlkft05ghc88gsea-2222...".
"""
import http.client
import http.server
import json
import os
import re
import socket
import socketserver

UPSTREAM = os.environ.get("UPSTREAM", "/var/run/docker.sock")
LISTEN = os.environ.get("LISTEN", "/proxy/docker.sock")
LIST_PATH = re.compile(r"^(/v[\d.]+)?/containers/json(\?|$)")
HOP = {"connection", "keep-alive", "transfer-encoding", "host"}


class UnixConn(http.client.HTTPConnection):
    def __init__(self):
        super().__init__("localhost", timeout=120)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(120)
        self.sock.connect(UPSTREAM)


def coolify_name(labels):
    resource = (labels.get("coolify.resourceName") or "").strip()
    sub = (labels.get("coolify.serviceName") or "").strip()
    if not resource:
        return None
    if labels.get("coolify.type") == "service" and sub and sub != resource:
        return f"{resource}/{sub}"
    return resource


def rename(body):
    containers = json.loads(body)
    by_name = {}
    for c in containers:
        name = coolify_name(c.get("Labels") or {})
        if name:
            by_name.setdefault(name, []).append(c)
    for name, group in by_name.items():
        # During a redeploy old and new containers share a name: the newest
        # keeps it, older ones get a short id suffix.
        group.sort(key=lambda c: c.get("Created", 0), reverse=True)
        for i, c in enumerate(group):
            label = name if i == 0 else f"{name}~{c['Id'][:6]}"
            c["Names"] = ["/" + label] + (c.get("Names") or [])
    return json.dumps(containers).encode()


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def refuse(self):
        self.send_response(405)
        self.send_header("Content-Length", "0")
        self.end_headers()

    do_POST = do_PUT = do_DELETE = do_PATCH = refuse

    def do_HEAD(self):
        self.forward()

    def do_GET(self):
        self.forward()

    def forward(self):
        up = UnixConn()
        try:
            headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP}
            up.request(self.command, self.path, headers=headers)
            resp = up.getresponse()

            if self.command == "GET" and resp.status == 200 and LIST_PATH.match(self.path):
                body = rename(resp.read())
                self.send_response(200)
                for k, v in resp.getheaders():
                    if k.lower() not in HOP and k.lower() != "content-length":
                        self.send_header(k, v)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return

            no_body = self.command == "HEAD" or resp.status in (204, 304) or resp.status < 200
            chunked = not no_body and resp.getheader("Content-Length") is None
            self.send_response(resp.status, resp.reason)
            for k, v in resp.getheaders():
                if k.lower() not in HOP:
                    self.send_header(k, v)
            if chunked:
                self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            if no_body:
                return
            while True:
                chunk = resp.read1(65536)
                if not chunk:
                    break
                self.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk) if chunked else chunk)
                self.wfile.flush()
            if chunked:
                self.wfile.write(b"0\r\n\r\n")
        except (BrokenPipeError, ConnectionResetError):
            self.close_connection = True
        except Exception as e:
            msg = json.dumps({"message": f"proxy: {e}"}).encode()
            try:
                self.send_response(502)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(msg)))
                self.end_headers()
                self.wfile.write(msg)
            except OSError:
                self.close_connection = True
        finally:
            up.close()


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


if __name__ == "__main__":
    if os.path.exists(LISTEN):
        os.unlink(LISTEN)
    os.makedirs(os.path.dirname(LISTEN), exist_ok=True)
    server = Server(LISTEN, Handler)
    os.chmod(LISTEN, 0o660)
    print(f"docker read-only proxy: {LISTEN} -> {UPSTREAM}", flush=True)
    server.serve_forever()