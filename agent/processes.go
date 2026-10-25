//go:build linux

package agent

import (
	"bytes"
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/henrygd/beszel/agent/utils"
	"github.com/henrygd/beszel/internal/entities/container"
	"github.com/henrygd/beszel/internal/entities/process"
)

// Process monitoring reads /proc directly, so it works on hosts where the
// workloads are plain processes rather than containers. Each update reports:
//
//   - groups: usage summed per systemd service, or per program for processes
//     outside a service, tagged with the user for processes in a user session.
//     They are shaped like container stats, so the hub stores, charts and
//     alerts on them the way it does for containers.
//   - a top list of individual processes (the heaviest by CPU and by memory),
//     the htop view.
//
// CPU is a percent of the whole host, averaged since the previous update for
// the same interval, matching the containers table.

const (
	procClockTicks = 100 // USER_HZ; fixed at 100 on every Linux ABI the agent supports
	procTopCount   = 25  // processes kept from each of the CPU and memory rankings
	procCmdMax     = 300 // characters of command line kept per process
	// groups under both of these are summed into procOtherGroup
	procGroupMinMemMB = 25
	procGroupMinCpu   = 0.5
	procOtherGroup    = "other"
)

type procSample struct {
	ticks uint64 // utime + stime
	start uint64 // start time in ticks since boot; tells a reused pid apart
}

type procSnapshot struct {
	at     time.Time
	uptime uint64 // ticks since boot when taken
	procs  map[int32]procSample
}

type processManager struct {
	sync.Mutex
	procDir  string
	pageSize uint64
	numCPU   float64
	bootTime int64
	prev     map[uint16]*procSnapshot // keyed by cache time, like docker stats
	users    map[uint32]string
}

func newProcessManager() *processManager {
	pm := &processManager{
		procDir:  "/proc",
		pageSize: uint64(os.Getpagesize()),
		numCPU:   float64(runtime.NumCPU()),
		prev:     make(map[uint16]*procSnapshot),
	}
	pm.bootTime = readBootTime(pm.procDir)
	pm.loadUsers()
	// prime the default interval so the first report has real CPU numbers
	pm.collect(defaultDataCacheTimeMs)
	return pm
}

// procInfo is one process as read in a single pass.
type procInfo struct {
	pid    int32
	uid    uint32
	name   string
	cmd    string
	group  string
	cpu    float64
	mem    uint64
	start  uint64
	ticks  uint64
	inUser bool
}

// collect returns the per-group stats and the top process list.
func (pm *processManager) collect(cacheTimeMs uint16) ([]*container.Stats, []*process.Proc) {
	pm.Lock()
	defer pm.Unlock()

	entries, err := os.ReadDir(pm.procDir)
	if err != nil {
		return nil, nil
	}
	now := time.Now()
	uptime := readUptimeTicks(pm.procDir)
	prev := pm.prev[cacheTimeMs]
	next := &procSnapshot{at: now, uptime: uptime, procs: make(map[int32]procSample, len(entries))}

	var elapsed float64
	if prev != nil {
		elapsed = now.Sub(prev.at).Seconds()
	}

	procs := make([]procInfo, 0, len(entries))
	unknownUser := false
	for _, e := range entries {
		pid64, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil {
			continue
		}
		pid := int32(pid64)
		p, ok := pm.readProc(pid)
		if !ok {
			continue
		}
		next.procs[pid] = procSample{ticks: p.ticks, start: p.start}

		if prev != nil && elapsed > 0 {
			var delta uint64
			if old, seen := prev.procs[pid]; seen && old.start == p.start {
				if p.ticks >= old.ticks {
					delta = p.ticks - old.ticks
				}
			} else if p.start >= prev.uptime {
				// started since the previous sample, so all its time is in this interval
				delta = p.ticks
			}
			p.cpu = float64(delta) / procClockTicks / elapsed / pm.numCPU * 100
		}
		if _, ok := pm.users[p.uid]; !ok {
			unknownUser = true
		}
		procs = append(procs, p)
	}
	pm.prev[cacheTimeMs] = next
	if unknownUser {
		pm.loadUsers()
	}

	for i := range procs {
		procs[i].group = pm.groupName(&procs[i])
	}
	return pm.groups(procs), pm.top(procs)
}

// readProc reads one process. Kernel threads and processes that exit while
// being read are skipped.
func (pm *processManager) readProc(pid int32) (procInfo, bool) {
	dir := filepath.Join(pm.procDir, strconv.Itoa(int(pid)))
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return procInfo{}, false
	}
	// comm may contain spaces and parentheses, so split on the last ")"
	open := bytes.IndexByte(stat, '(')
	end := bytes.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return procInfo{}, false
	}
	comm := string(stat[open+1 : end])
	// fields after comm start at state (field 3)
	f := strings.Fields(string(stat[end+1:]))
	if len(f) < 22 {
		return procInfo{}, false
	}
	ppid, _ := strconv.Atoi(f[1])
	if pid == 2 || ppid == 2 {
		return procInfo{}, false // kernel thread
	}
	utime, _ := strconv.ParseUint(f[11], 10, 64)
	stime, _ := strconv.ParseUint(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	rss, _ := strconv.ParseUint(f[21], 10, 64)

	p := procInfo{
		pid:   pid,
		ticks: utime + stime,
		start: start,
		mem:   rss * pm.pageSize,
	}
	if fi, err := os.Stat(dir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			p.uid = st.Uid
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		p.name, p.cmd = parseCmdline(raw)
	}
	if p.name == "" {
		p.name = comm
	}
	if cg, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		p.group, p.inUser = serviceFromCgroup(string(cg))
	}
	return p, true
}

// groupName is the service the process runs in, or its program name, tagged
// with the user for processes in a user's session ("claude (hammas)").
func (pm *processManager) groupName(p *procInfo) string {
	base := p.group
	if base == "" {
		base = p.name
	}
	if p.inUser && p.uid != 0 {
		return base + " (" + pm.userName(p.uid) + ")"
	}
	return base
}

func (pm *processManager) userName(uid uint32) string {
	if name, ok := pm.users[uid]; ok {
		return name
	}
	return strconv.FormatUint(uint64(uid), 10)
}

func (pm *processManager) groups(procs []procInfo) []*container.Stats {
	type sum struct {
		cpu float64
		mem uint64
	}
	sums := make(map[string]*sum)
	for i := range procs {
		s := sums[procs[i].group]
		if s == nil {
			s = &sum{}
			sums[procs[i].group] = s
		}
		s.cpu += procs[i].cpu
		s.mem += procs[i].mem
	}
	out := make([]*container.Stats, 0, len(sums))
	other := &container.Stats{Name: procOtherGroup}
	for name, s := range sums {
		memMB := float64(s.mem) / 1024 / 1024
		if memMB < procGroupMinMemMB && s.cpu < procGroupMinCpu {
			other.Cpu += s.cpu
			other.Mem += memMB
			continue
		}
		out = append(out, &container.Stats{Name: name, Cpu: utils.TwoDecimals(s.cpu), Mem: utils.TwoDecimals(memMB)})
	}
	if other.Mem > 0 || other.Cpu > 0 {
		other.Cpu = utils.TwoDecimals(other.Cpu)
		other.Mem = utils.TwoDecimals(other.Mem)
		out = append(out, other)
	}
	slices.SortFunc(out, func(a, b *container.Stats) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// top keeps the heaviest processes by CPU and by memory.
func (pm *processManager) top(procs []procInfo) []*process.Proc {
	keep := make(map[int32]bool, procTopCount*2)
	slices.SortFunc(procs, func(a, b procInfo) int { return cmp.Compare(b.cpu, a.cpu) })
	for i := 0; i < len(procs) && i < procTopCount; i++ {
		if procs[i].cpu > 0 {
			keep[procs[i].pid] = true
		}
	}
	slices.SortFunc(procs, func(a, b procInfo) int { return cmp.Compare(b.mem, a.mem) })
	for i := 0; i < len(procs) && i < procTopCount; i++ {
		keep[procs[i].pid] = true
	}
	out := make([]*process.Proc, 0, len(keep))
	for i := range procs {
		p := &procs[i]
		if !keep[p.pid] {
			continue
		}
		proc := &process.Proc{
			Pid:   p.pid,
			User:  pm.userName(p.uid),
			Name:  p.name,
			Cmd:   p.cmd,
			Group: p.group,
			Cpu:   utils.TwoDecimals(p.cpu),
			Mem:   p.mem,
		}
		if pm.bootTime > 0 {
			proc.Start = pm.bootTime + int64(p.start/procClockTicks)
		}
		out = append(out, proc)
	}
	return out
}

func (pm *processManager) loadUsers() {
	users := make(map[uint32]string)
	if data, err := os.ReadFile("/etc/passwd"); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			f := strings.Split(line, ":")
			if len(f) < 3 {
				continue
			}
			if uid, err := strconv.ParseUint(f[2], 10, 32); err == nil {
				users[uint32(uid)] = f[0]
			}
		}
	}
	pm.users = users
}

// parseCmdline returns the program name and the command line. Programs that
// rewrite their title ("postgres: writer", "sshd: hammas@pts/0") have one
// space-separated string instead of NUL-separated arguments.
func parseCmdline(raw []byte) (name, cmd string) {
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return "", ""
	}
	args := strings.Split(string(raw), "\x00")
	first := args[0]
	if len(args) == 1 {
		first, _, _ = strings.Cut(first, " ")
	}
	name = strings.TrimSuffix(filepath.Base(first), ":")
	cmd = strings.Join(args, " ")
	if len(cmd) > procCmdMax {
		cmd = cmd[:procCmdMax]
	}
	return name, cmd
}

// serviceFromCgroup returns the systemd service a process runs in, without
// the ".service" suffix, and whether it is inside a user's slice. A user's
// systemd manager (user@1000.service) is not a service of its own: processes
// directly in it, or in a scope under it (tmux, terminals), have no service.
func serviceFromCgroup(cgroup string) (service string, inUser bool) {
	var path string
	for line := range strings.SplitSeq(cgroup, "\n") {
		// cgroup v2 is "0::<path>"; on v1 hosts use the systemd hierarchy
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			path = p
			break
		}
		if _, p, ok := strings.Cut(line, ":name=systemd:"); ok {
			path = p
		}
	}
	inUser = strings.HasPrefix(path, "/user.slice/")
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if strings.HasSuffix(seg, ".scope") {
			return "", inUser
		}
		if strings.HasSuffix(seg, ".service") {
			if strings.HasPrefix(seg, "user@") {
				return "", inUser
			}
			return strings.TrimSuffix(seg, ".service"), inUser
		}
	}
	return "", inUser
}

func readBootTime(procDir string) int64 {
	data, err := os.ReadFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			t, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return t
		}
	}
	return 0
}

func readUptimeTicks(procDir string) uint64 {
	data, err := os.ReadFile(filepath.Join(procDir, "uptime"))
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	secs, _ := strconv.ParseFloat(f[0], 64)
	return uint64(secs * procClockTicks)
}
