//go:build linux

package agent

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestServiceFromCgroup(t *testing.T) {
	cases := []struct {
		cgroup  string
		service string
		inUser  bool
	}{
		{"0::/system.slice/tailscaled.service\n", "tailscaled", false},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/cutroom.service\n", "cutroom", true},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/tmux-spawn-62cc.scope\n", "", true},
		{"0::/user.slice/user-1000.slice/user@1000.service/init.scope\n", "", true},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", "", true},
		{"0::/system.slice/docker.service/sub\n", "docker", false},
		{"12:cpu:/x\n1:name=systemd:/system.slice/cron.service\n", "cron", false},
		{"", "", false},
	}
	for _, c := range cases {
		service, inUser := serviceFromCgroup(c.cgroup)
		assert.Equal(t, c.service, service, c.cgroup)
		assert.Equal(t, c.inUser, inUser, c.cgroup)
	}
}

func TestParseCmdline(t *testing.T) {
	name, cmd := parseCmdline([]byte("/usr/bin/node\x00/srv/app/vite\x00--port\x003000\x00"))
	assert.Equal(t, "node", name)
	assert.Equal(t, "/usr/bin/node /srv/app/vite --port 3000", cmd)

	name, _ = parseCmdline([]byte("postgres: checkpointer process\x00"))
	assert.Equal(t, "postgres", name)

	name, cmd = parseCmdline(nil)
	assert.Empty(t, name)
	assert.Empty(t, cmd)
}

// TestProcessCollectLive runs against the real /proc; set PROC_LIVE=1 to print it.
func TestProcessCollectLive(t *testing.T) {
	if os.Getenv("PROC_LIVE") == "" {
		t.Skip("set PROC_LIVE=1")
	}
	pm := newProcessManager()
	pm.collect(1000)
	time.Sleep(2 * time.Second)
	groups, procs := pm.collect(1000)
	var cpu float64
	for _, g := range groups {
		cpu += g.Cpu
		t.Logf("group %-40s cpu=%6.2f%% mem=%8.1fMB", g.Name, g.Cpu, g.Mem)
	}
	t.Logf("total cpu %.2f%% across %d groups, %d top procs", cpu, len(groups), len(procs))
	for _, p := range procs[:min(10, len(procs))] {
		t.Logf("proc %7d %-8s %-12s cpu=%6.2f%% mem=%6dMB %s", p.Pid, p.User, p.Name, p.Cpu, p.Mem>>20, p.Group)
	}
	assert.NotEmpty(t, groups)
	assert.NotEmpty(t, procs)
}
