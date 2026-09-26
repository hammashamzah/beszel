"""Per-app CPU and memory alerts for Coolify apps, sent to Discord.

Beszel alerts on the host as a whole. This covers the two rules it has no
equivalent for, carried over from the Grafana stack it replaced:

- an app uses more than CPU_LIMIT_PERCENT of a core (200 = two cores)
- an app uses more than MEMORY_LIMIT_GB of memory

each sustained for FOR_MINUTES. An app is a Coolify resource, with all of its
containers summed. Memory excludes reclaimable file cache, the same figure
`docker stats` and Beszel show. It posts again when the app recovers.

Reads Docker through the read-only proxy socket, so it never holds the Docker
socket itself.
"""
import http.client
import json
import os
import socket
import sys
import time
import urllib.request

DOCKER_SOCK = os.environ.get("DOCKER_SOCK", "/proxy/docker.sock")
WEBHOOK = os.environ.get("DISCORD_WEBHOOK_URL", "")
CPU_LIMIT = float(os.environ.get("CPU_LIMIT_PERCENT", "200"))
MEMORY_LIMIT_GB = float(os.environ.get("MEMORY_LIMIT_GB", "2"))
FOR_SECONDS = float(os.environ.get("FOR_MINUTES", "10")) * 60
INTERVAL = float(os.environ.get("INTERVAL_SECONDS", "60"))
HOST = os.environ.get("HOST_LABEL") or socket.gethostname()
DRY_RUN = os.environ.get("DRY_RUN") == "1"
HEARTBEAT = "/tmp/app-alerts-heartbeat"

LIMITS = {"cpu": CPU_LIMIT, "memory": MEMORY_LIMIT_GB}
RED, GREEN = 0xE5484D, 0x30A46C


def log(msg):
    print(time.strftime("%Y-%m-%d %H:%M:%S"), msg, flush=True)


class UnixConn(http.client.HTTPConnection):
    def __init__(self):
        super().__init__("localhost", timeout=30)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(30)
        self.sock.connect(DOCKER_SOCK)


def docker_get(path):
    conn = UnixConn()
    try:
        conn.request("GET", path)
        resp = conn.getresponse()
        body = resp.read()
    finally:
        conn.close()
    if resp.status != 200:
        raise RuntimeError(f"GET {path}: HTTP {resp.status}")
    return json.loads(body)


def sample():
    """{app: {container id: (cpu ns used so far, memory bytes)}} for running Coolify containers."""
    apps = {}
    for c in docker_get("/containers/json"):
        app = (c.get("Labels") or {}).get("coolify.resourceName")
        if not app:
            continue
        try:
            s = docker_get(f"/containers/{c['Id']}/stats?stream=0&one-shot=1")
        except Exception as e:  # container went away between list and stats
            log(f"skip {app} {c['Id'][:12]}: {e}")
            continue
        cpu = s.get("cpu_stats", {}).get("cpu_usage", {}).get("total_usage", 0)
        mem = s.get("memory_stats", {})
        stats = mem.get("stats", {})
        cache = stats.get("inactive_file", stats.get("total_inactive_file", 0))
        apps.setdefault(app, {})[c["Id"]] = (cpu, max(0, mem.get("usage", 0) - cache))
    return apps


def usage(prev, cur, elapsed_s):
    """{(app, "cpu"|"memory"): value} from two samples. CPU is percent of one core."""
    values = {}
    for app, containers in cur.items():
        before = prev.get(app, {})
        # only containers seen in both samples; a new container has no rate yet
        cpu_ns = sum(
            now[0] - before[cid][0]
            for cid, now in containers.items()
            if cid in before and now[0] >= before[cid][0]
        )
        values[(app, "cpu")] = cpu_ns / (elapsed_s * 1e9) * 100
        values[(app, "memory")] = sum(m for _, m in containers.values()) / 1e9
    return values


def describe(kind, value):
    if kind == "cpu":
        return f"CPU {value:.0f}% of a core (limit {CPU_LIMIT:.0f}%)"
    return f"memory {value:.2f} GB (limit {MEMORY_LIMIT_GB:g} GB)"


class Alerts:
    def __init__(self, notify):
        self.notify = notify
        self.breach_since = {}  # key -> first time it was seen over the limit
        self.firing = set()

    def evaluate(self, values, now):
        for key, value in values.items():
            app, kind = key
            if value > LIMITS[kind]:
                since = self.breach_since.setdefault(key, now)
                if key not in self.firing and now - since >= FOR_SECONDS:
                    self.firing.add(key)
                    minutes = round((now - since) / 60)
                    self.notify(RED, f"{app}: high {kind}", f"**{app}** on {HOST}: {describe(kind, value)} for {minutes} min")
            else:
                self.breach_since.pop(key, None)
                if key in self.firing:
                    self.firing.discard(key)
                    self.notify(GREEN, f"{app}: {kind} back to normal", f"**{app}** on {HOST}: {describe(kind, value)}")
        for key in list(self.breach_since):
            if key not in values:
                self.breach_since.pop(key)
        for key in list(self.firing):
            if key not in values:
                self.firing.discard(key)
                self.notify(GREEN, f"{key[0]}: no longer running", f"**{key[0]}** on {HOST} stopped, so its {key[1]} alert is cleared")


def discord(color, title, text):
    log(f"alert: {title} | {text}")
    if DRY_RUN or not WEBHOOK:
        return
    body = json.dumps({"embeds": [{"title": title, "description": text, "color": color}]}).encode()
    req = urllib.request.Request(WEBHOOK, data=body, headers={"Content-Type": "application/json", "User-Agent": "beszel-app-alerts"})
    try:
        urllib.request.urlopen(req, timeout=15).close()
    except Exception as e:
        log(f"discord post failed: {e}")


def healthcheck():
    try:
        age = time.time() - os.path.getmtime(HEARTBEAT)
    except OSError:
        sys.exit(1)
    sys.exit(0 if age < INTERVAL * 3 + 60 else 1)


def main():
    if not WEBHOOK and not DRY_RUN:
        log("DISCORD_WEBHOOK_URL is not set; alerts will only be logged")
    log(f"watching {DOCKER_SOCK}: cpu > {CPU_LIMIT:g}%, memory > {MEMORY_LIMIT_GB:g} GB, for {FOR_SECONDS / 60:g} min")
    alerts = Alerts(discord)
    prev = prev_t = None
    while True:
        started = time.time()
        try:
            cur = sample()
        except Exception as e:
            log(f"sample failed: {e}")
        else:
            if prev is not None:
                alerts.evaluate(usage(prev, cur, started - prev_t), started)
            prev, prev_t = cur, started
            with open(HEARTBEAT, "w") as f:
                f.write(str(started))
        time.sleep(max(1.0, INTERVAL - (time.time() - started)))


if __name__ == "__main__":
    if sys.argv[1:] == ["--healthcheck"]:
        healthcheck()
    main()
