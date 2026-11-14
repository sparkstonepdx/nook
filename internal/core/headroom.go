package core

// Headroom: is this server big enough for what runs on it, and if not, what to add.
//
// The root helper takes a sample every 5 minutes (sampleNow) and appends it to /var/lib/nook/stats/
// samples.jsonl, keeping 30 days. Headroom turns those into one sentence, a status per resource, advice
// rows and the series the dashboard draws. No Prometheus: /proc, statfs and systemd's view of each app's
// slice are enough.
//
// The signals that matter on a small server, and why:
//   - memory: the kernel's pressure figure (PSI, /proc/pressure/memory), the share of time programs stalled
//     waiting for memory. It shows a server that's short on RAM before anything is killed. Plus available
//     memory and swap in use.
//   - CPU: CPU pressure, the share of time runnable work waited for a core.
//   - disk: free space where apps and nook's state live, and days until full at the last week's rate.
//   - each app: memory against its own limit, and kills by that limit (memory.events oom_kill), which mean
//     the app's limit is too low, not the server.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/update"
)

const (
	sampleEvery = 5 * time.Minute
	keepSamples = 30 * 24 * time.Hour
)

func statsDir() string    { return filepath.Join(config.LibDir, "stats") }
func samplesPath() string { return filepath.Join(statsDir(), "samples.jsonl") }

type DiskSample struct {
	Path   string `json:"path"`
	Total  uint64 `json:"total"`
	Free   uint64 `json:"free"` // available to unprivileged users
	Inodes uint64 `json:"inodes"`
	IFree  uint64 `json:"ifree"`
}

type AppSample struct {
	Mem    uint64 `json:"mem"`
	MemMax uint64 `json:"memMax,omitempty"` // 0: no limit
	CPUNs  uint64 `json:"cpuNs"`
	OOM    uint64 `json:"oom"` // kills by its own memory limit, a running count
}

type Sample struct {
	T         int64                `json:"t"`
	MemTotal  uint64               `json:"memTotal"`
	MemAvail  uint64               `json:"memAvail"`
	SwapTotal uint64               `json:"swapTotal"`
	SwapFree  uint64               `json:"swapFree"`
	PSIMem    float64              `json:"psiMem"` // % of the last minute some task stalled on memory
	PSICPU    float64              `json:"psiCpu"`
	PSIIO     float64              `json:"psiIo"`
	Load1     float64              `json:"load1"`
	CPUs      int                  `json:"cpus"`
	Disks     []DiskSample         `json:"disks"`
	Apps      map[string]AppSample `json:"apps"`
}

// ---- taking samples ----

func readMeminfo() map[string]uint64 {
	m := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) > 0 {
			n, _ := strconv.ParseUint(fs[0], 10, 64)
			m[k] = n * 1024 // kB
		}
	}
	return m
}

// psi reads "some avg60" from /proc/pressure/KIND: 0 where the kernel doesn't report it.
func psi(kind string) float64 {
	b, err := os.ReadFile("/proc/pressure/" + kind)
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "some ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(f, "avg60="); ok {
				n, _ := strconv.ParseFloat(v, 64)
				return n
			}
		}
	}
	return 0
}

func diskSample(path string) (DiskSample, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskSample{}, false
	}
	bs := uint64(st.Bsize)
	return DiskSample{Path: path, Total: st.Blocks * bs, Free: st.Bavail * bs, Inodes: st.Files, IFree: st.Ffree}, true
}

// diskPaths are the filesystems nook's data lives on, one entry per filesystem.
func diskPaths() []string {
	seen := map[uint64]bool{}
	var out []string
	for _, p := range []string{config.AppsDir, filepath.Dir(config.WWWDir), "/"} {
		var st syscall.Stat_t
		if syscall.Stat(p, &st) != nil || seen[st.Dev] {
			continue
		}
		seen[st.Dev] = true
		out = append(out, p)
	}
	return out
}

func appSample(app string) AppSample {
	p := UnitProps("nook-"+app+".slice", "MemoryCurrent", "MemoryMax", "CPUUsageNSec", "ControlGroup")
	num := func(k string) uint64 { n, _ := strconv.ParseUint(p[k], 10, 64); return n } // "[not set]", "infinity" -> 0
	a := AppSample{Mem: num("MemoryCurrent"), MemMax: num("MemoryMax"), CPUNs: num("CPUUsageNSec")}
	if cg := p["ControlGroup"]; cg != "" {
		if b, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", cg, "memory.events")); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "oom_kill "); ok {
					a.OOM, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64)
				}
			}
		}
	}
	return a
}

func sampleNow() Sample {
	mi := readMeminfo()
	s := Sample{T: time.Now().Unix(), MemTotal: mi["MemTotal"], MemAvail: mi["MemAvailable"], SwapTotal: mi["SwapTotal"], SwapFree: mi["SwapFree"],
		PSIMem: psi("memory"), PSICPU: psi("cpu"), PSIIO: psi("io"), CPUs: numCPU(), Apps: map[string]AppSample{}}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	for _, p := range diskPaths() {
		if d, ok := diskSample(p); ok {
			s.Disks = append(s.Disks, d)
		}
	}
	for _, a := range Apps() {
		s.Apps[a] = appSample(a)
	}
	return s
}

func numCPU() int { return runtime.NumCPU() }

// RunSampler takes a sample now and every 5 minutes, keeps 30 days of them, and tells you when the
// headroom advice changes. Run inside the helper.
func RunSampler() {
	for {
		recordSample(sampleNow())
		notifyIfChanged()
		time.Sleep(sampleEvery)
	}
}

func recordSample(s Sample) {
	os.MkdirAll(statsDir(), 0o755)
	b, _ := json.Marshal(s)
	f, err := os.OpenFile(samplesPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
	f.Close()
	// once the file holds more than 31 days, rewrite it with the last 30
	if st, err := os.Stat(samplesPath()); err == nil && st.Size() > 8<<20 {
		all := readSamples(keepSamples)
		var out []byte
		for _, x := range all {
			b, _ := json.Marshal(x)
			out = append(append(out, b...), '\n')
		}
		config.WriteAtomic(samplesPath(), out, 0o644)
	}
}

func readSamples(since time.Duration) []Sample {
	f, err := os.Open(samplesPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	cut := time.Now().Add(-since).Unix()
	var out []Sample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var s Sample
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.T >= cut {
			out = append(out, s)
		}
	}
	return out
}

// ---- reading them ----

type ResourceStatus struct {
	Name   string  `json:"name"`   // Memory, CPU, Disk
	Status string  `json:"status"` // ok, watch, tight
	Used   float64 `json:"used"`   // 0-1, now
	Figure string  `json:"figure"` // "2.1 of 4 GB", "12% waiting", "31 of 80 GB"
	Detail string  `json:"detail"`
}

type Advice struct {
	Level  string `json:"level"` // watch, tight
	Title  string `json:"title"` // "Add RAM"
	Detail string `json:"detail"`
	App    string `json:"app,omitempty"`
}

type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type Series struct {
	Name   string  `json:"name"`
	Unit   string  `json:"unit"` // "%"
	Points []Point `json:"points"`
	Max    float64 `json:"max"` // the axis' top: 100 for a share
}

type HeadroomView struct {
	Status    string           `json:"status"`  // ok, watch, tight, or "learning" with too few samples
	Summary   string           `json:"summary"` // one sentence
	Resources []ResourceStatus `json:"resources"`
	Advice    []Advice         `json:"advice"`
	Series    []Series         `json:"series"`
	Since     int64            `json:"since,omitempty"` // the oldest sample shown
	Samples   int              `json:"samples"`
}

func gb(n uint64) string {
	g := float64(n) / (1 << 30)
	if g < 10 {
		return strconv.FormatFloat(math.Round(g*10)/10, 'f', -1, 64)
	}
	return strconv.FormatFloat(math.Round(g), 'f', -1, 64)
}

func mib(n uint64) string { return strconv.FormatUint(n>>20, 10) + " MB" }

var rank = map[string]int{"ok": 0, "watch": 1, "tight": 2}

func worse(a, b string) string {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Headroom analyses the last day (for the status and advice) and draws the given range.
func Headroom(rng string) HeadroomView {
	span := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}[rng]
	if span == 0 {
		span = 24 * time.Hour
	}
	all := readSamples(keepSamples)
	v := analyse(all, time.Now())
	v.Series, v.Since = series(all, span, time.Now())
	return v
}

func analyse(all []Sample, now time.Time) HeadroomView {
	v := HeadroomView{Status: "ok", Resources: []ResourceStatus{}, Advice: []Advice{}, Series: []Series{}, Samples: len(all)}
	if len(all) == 0 {
		v.Status, v.Summary = "learning", "Measuring this server. The first figures appear within 5 minutes."
		return v
	}
	day := since(all, now, 24*time.Hour)
	last := all[len(all)-1]

	// memory
	mem := ResourceStatus{Name: "Memory", Status: "ok"}
	if last.MemTotal > 0 {
		used := last.MemTotal - last.MemAvail
		mem.Used = float64(used) / float64(last.MemTotal)
		mem.Figure = gb(used) + " of " + gb(last.MemTotal) + " GB"
		maxPSI, minAvail := 0.0, 1.0
		for _, s := range day {
			maxPSI = math.Max(maxPSI, s.PSIMem)
			if s.MemTotal > 0 {
				minAvail = math.Min(minAvail, float64(s.MemAvail)/float64(s.MemTotal))
			}
		}
		swapUsed := 0.0
		if last.SwapTotal > 0 {
			swapUsed = float64(last.SwapTotal-last.SwapFree) / float64(last.SwapTotal)
		}
		switch {
		case maxPSI >= 10 || minAvail < 0.05 || swapUsed > 0.5:
			mem.Status = "tight"
		case maxPSI >= 2 || minAvail < 0.15:
			mem.Status = "watch"
		}
		mem.Detail = fmt.Sprintf("Programs waited for memory up to %.0f%% of a minute today; at its fullest, %.0f%% was free.", maxPSI, minAvail*100)
		if maxPSI < 0.5 {
			mem.Detail = fmt.Sprintf("Nothing waited for memory today; at its fullest, %.0f%% was free.", minAvail*100)
		}
		if swapUsed > 0.05 {
			mem.Detail += fmt.Sprintf(" %.0f%% of swap is in use.", swapUsed*100)
		}
		switch mem.Status {
		case "tight":
			v.Advice = append(v.Advice, Advice{Level: "tight", Title: "Add RAM", Detail: mem.Detail + " Apps slow down or get killed like this; more memory is the fix, or move an app to another server."})
		case "watch":
			v.Advice = append(v.Advice, Advice{Level: "watch", Title: "Memory is getting full", Detail: mem.Detail + " Fine for now; if it keeps climbing, add RAM."})
		}
	}
	v.Resources = append(v.Resources, mem)

	// CPU
	cpu := ResourceStatus{Name: "CPU", Status: "ok", Used: last.PSICPU / 100, Figure: fmt.Sprintf("%.0f%% waiting", last.PSICPU)}
	busy, veryBusy := 0, 0
	for _, s := range day {
		if s.PSICPU >= 10 {
			busy++
		}
		if s.PSICPU >= 25 {
			veryBusy++
		}
	}
	if n := len(day); n > 0 {
		switch {
		case float64(veryBusy)/float64(n) > 0.25:
			cpu.Status = "tight"
		case float64(busy)/float64(n) > 0.10:
			cpu.Status = "watch"
		}
		hours := float64(busy) * sampleEvery.Hours()
		cpu.Detail = fmt.Sprintf("Work waited for a core at least a tenth of the time for %s today, on %d %s.", plural(int(math.Round(hours)), "hour"), last.CPUs, map[bool]string{true: "core", false: "cores"}[last.CPUs == 1])
		if busy == 0 {
			cpu.Detail = fmt.Sprintf("Work rarely waited for a core today, on %d %s.", last.CPUs, map[bool]string{true: "core", false: "cores"}[last.CPUs == 1])
		}
		switch cpu.Status {
		case "tight":
			v.Advice = append(v.Advice, Advice{Level: "tight", Title: "Add CPU", Detail: cpu.Detail + " Requests queue up like this; more cores would answer them sooner."})
		case "watch":
			v.Advice = append(v.Advice, Advice{Level: "watch", Title: "The CPU is often busy", Detail: cpu.Detail})
		}
	}
	v.Resources = append(v.Resources, cpu)

	// disk: the fullest filesystem nook uses
	if len(last.Disks) > 0 {
		d := last.Disks[0]
		for _, x := range last.Disks[1:] {
			if x.Total > 0 && float64(x.Free)/float64(x.Total) < float64(d.Free)/float64(d.Total) {
				d = x
			}
		}
		disk := ResourceStatus{Name: "Disk", Status: "ok"}
		if d.Total > 0 {
			used := d.Total - d.Free
			disk.Used = float64(used) / float64(d.Total)
			disk.Figure = gb(used) + " of " + gb(d.Total) + " GB"
			free := float64(d.Free) / float64(d.Total)
			days := daysUntilFull(since(all, now, 7*24*time.Hour), d.Path)
			inodeFree := 1.0
			if d.Inodes > 0 {
				inodeFree = float64(d.IFree) / float64(d.Inodes)
			}
			switch {
			case free < 0.10 || (days >= 0 && days < 14) || inodeFree < 0.05:
				disk.Status = "tight"
			case free < 0.20 || (days >= 0 && days < 60) || inodeFree < 0.15:
				disk.Status = "watch"
			}
			disk.Detail = fmt.Sprintf("%.0f%% free.", free*100)
			switch {
			case days >= 0:
				disk.Detail += " At this week's rate it's full in about " + humanDays(days) + "."
			default:
				disk.Detail += " It isn't filling up."
			}
			if inodeFree < 0.15 {
				disk.Detail += fmt.Sprintf(" Only %.0f%% of its file slots (inodes) are left: something is making very many small files.", inodeFree*100)
			}
			switch disk.Status {
			case "tight":
				v.Advice = append(v.Advice, Advice{Level: "tight", Title: "Add storage", Detail: disk.Detail + " A full disk stops deploys, logs and databases."})
			case "watch":
				v.Advice = append(v.Advice, Advice{Level: "watch", Title: "Storage is filling", Detail: disk.Detail})
			}
		}
		v.Resources = append(v.Resources, disk)
	}

	// each app: killed by its own limit, or close to it
	first := map[string]uint64{}
	for _, s := range day {
		for a, x := range s.Apps {
			if _, ok := first[a]; !ok {
				first[a] = x.OOM
			}
		}
	}
	apps := make([]string, 0, len(last.Apps))
	for a := range last.Apps {
		apps = append(apps, a)
	}
	sort.Strings(apps)
	for _, a := range apps {
		x := last.Apps[a]
		if kills := x.OOM - first[a]; x.OOM >= first[a] && kills > 0 {
			v.Advice = append(v.Advice, Advice{Level: "tight", App: a, Title: "Raise " + a + "'s memory limit",
				Detail: fmt.Sprintf("Its %s limit stopped it %s today. That's its own limit, not the server's: raise it on its Manage page (or nook-admin limits "+a+" --memory ...).", mib(x.MemMax), plural(int(kills), "time"))})
		} else if x.MemMax > 0 && float64(x.Mem)/float64(x.MemMax) > 0.9 {
			v.Advice = append(v.Advice, Advice{Level: "watch", App: a, Title: a + " is near its memory limit",
				Detail: fmt.Sprintf("Using %s of its %s. If it grows, it'll be stopped; raise the limit before then.", mib(x.Mem), mib(x.MemMax))})
		}
	}

	for _, r := range v.Resources {
		v.Status = worse(v.Status, r.Status)
	}
	for _, a := range v.Advice {
		v.Status = worse(v.Status, a.Level)
	}
	v.Summary = summary(v, last)
	return v
}

func summary(v HeadroomView, last Sample) string {
	if v.Status == "ok" {
		parts := []string{}
		for _, r := range v.Resources {
			if r.Name != "CPU" && r.Figure != "" {
				parts = append(parts, strings.ToLower(r.Name)+" "+r.Figure)
			}
		}
		return "Plenty of headroom: " + strings.Join(parts, ", ") + "."
	}
	titles := []string{}
	for _, a := range v.Advice {
		if a.Level == v.Status {
			titles = append(titles, strings.ToLower(a.Title[:1])+a.Title[1:])
		}
	}
	if v.Status == "tight" {
		return "This server needs attention: " + strings.Join(titles, ", ") + "."
	}
	return "Fine for now. Keep an eye on it: " + strings.Join(titles, ", ") + "."
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func humanDays(d float64) string {
	switch {
	case d < 2:
		return "a day"
	case d < 14:
		return plural(int(math.Round(d)), "day")
	case d < 60:
		return plural(int(math.Round(d/7)), "week")
	case d < 730:
		return plural(int(math.Round(d/30)), "month")
	}
	return plural(int(math.Round(d/365)), "year")
}

func since(all []Sample, now time.Time, d time.Duration) []Sample {
	cut := now.Add(-d).Unix()
	i := sort.Search(len(all), func(i int) bool { return all[i].T >= cut })
	return all[i:]
}

// daysUntilFull fits a line through the used bytes of path's filesystem and says when it reaches the
// total. -1: not growing, or under a day of history.
func daysUntilFull(week []Sample, path string) float64 {
	var xs, ys []float64
	var total float64
	for _, s := range week {
		for _, d := range s.Disks {
			if d.Path == path && d.Total > 0 {
				xs, ys = append(xs, float64(s.T)), append(ys, float64(d.Total-d.Free))
				total = float64(d.Total)
			}
		}
	}
	if len(xs) < 2 || xs[len(xs)-1]-xs[0] < 86400 {
		return -1
	}
	var mx, my float64
	for i := range xs {
		mx, my = mx+xs[i], my+ys[i]
	}
	mx, my = mx/float64(len(xs)), my/float64(len(xs))
	var num, den float64
	for i := range xs {
		num += (xs[i] - mx) * (ys[i] - my)
		den += (xs[i] - mx) * (xs[i] - mx)
	}
	if den == 0 {
		return -1
	}
	slope := num / den             // bytes per second
	if slope <= total*1e-9/86400 { // under a millionth of the disk a day: flat
		return -1
	}
	return (total - ys[len(ys)-1]) / slope / 86400
}

// series is what the dashboard draws: memory used, CPU waiting and disk used, as shares, averaged into at
// most 200 points.
func series(all []Sample, span time.Duration, now time.Time) ([]Series, int64) {
	in := since(all, now, span)
	if len(in) == 0 {
		return []Series{}, 0
	}
	buckets := int(math.Min(200, float64(len(in))))
	step := span.Seconds() / float64(buckets)
	start := float64(now.Add(-span).Unix())
	type acc struct{ n, mem, cpu, disk float64 }
	acs := make([]acc, buckets)
	for _, s := range in {
		i := int((float64(s.T) - start) / step)
		if i < 0 || i >= buckets {
			continue
		}
		a := &acs[i]
		a.n++
		if s.MemTotal > 0 {
			a.mem += float64(s.MemTotal-s.MemAvail) / float64(s.MemTotal) * 100
		}
		a.cpu += s.PSICPU
		if len(s.Disks) > 0 && s.Disks[0].Total > 0 {
			a.disk += float64(s.Disks[0].Total-s.Disks[0].Free) / float64(s.Disks[0].Total) * 100
		}
	}
	mk := func(name string, pick func(acc) float64) Series {
		sr := Series{Name: name, Unit: "%", Max: 100, Points: []Point{}}
		for i, a := range acs {
			if a.n > 0 {
				sr.Points = append(sr.Points, Point{T: int64(start + (float64(i)+0.5)*step), V: math.Round(pick(a)/a.n*10) / 10})
			}
		}
		return sr
	}
	return []Series{
		mk("Memory used", func(a acc) float64 { return a.mem }),
		mk("CPU waiting", func(a acc) float64 { return a.cpu }),
		mk("Disk used", func(a acc) float64 { return a.disk }),
	}, in[0].T
}

// notifyIfChanged sends one notification when the headroom status changes, not on every sample.
func notifyIfChanged() {
	v := analyse(readSamples(keepSamples), time.Now())
	if v.Status == "learning" {
		return
	}
	file := filepath.Join(statsDir(), "last-status")
	prev, _ := os.ReadFile(file)
	if string(prev) == v.Status {
		return
	}
	os.WriteFile(file, []byte(v.Status), 0o644)
	if len(prev) == 0 && v.Status == "ok" {
		return // the first look: nothing to report
	}
	update.Notify("nook: "+map[string]string{"ok": "plenty of headroom again", "watch": "server getting full", "tight": "server needs attention"}[v.Status], v.Summary)
}
