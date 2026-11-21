package core

// Per-app memory over time, with a projection, so a slow leak (a sync server creeping up a few MB a day) is seen days
// before the app hits its limit. The trend is a straight line through each hour's lowest reading since the app
// last restarted (up to 7 days): the floor is what a leak raises, and lunchtime spikes don't tilt it.

import (
	"fmt"

	"github.com/sparkstonepdx/nook/internal/config"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AppTrend struct {
	App        string  `json:"app"`
	Mem        uint64  `json:"mem"`
	Limit      uint64  `json:"limit"`      // its own limit, or the server's memory when it has none
	OwnLimit   bool    `json:"ownLimit"`   // Limit is the app's own (MemoryMax), not the server's
	CPU        float64 `json:"cpu"`        // % of one core, averaged over the last day
	CPULimit   float64 `json:"cpuLimit"`   // its CPUQuota as % of one core; 0: none
	CPUPoints  []Point `json:"cpuPoints"`  // % of one core for the chart, at most 120 points over the range
	PerDay     float64 `json:"perDay"`     // bytes a day its floor is rising (0: flat or falling, or not enough data)
	DaysLeft   float64 `json:"daysLeft"`   // until the floor reaches Limit; -1: not heading there
	FullAt     int64   `json:"fullAt"`     // unix time of that, or 0
	Kills      uint64  `json:"kills"`      // stopped by its own limit in the last 7 days
	Points     []Point `json:"points"`     // memory in MB for the chart, at most 120 points over the range
	Projection []Point `json:"projection"` // the trend line from now to FullAt or 7 days ahead, in MB

	// Disk: everything nook keeps for the app (appdisk.go), its growth over the last week, and when the disk it
	// lives on would be full at that rate. No disk fields until the first hourly measurement.
	Disk           uint64   `json:"disk"`
	DiskParts      *AppDisk `json:"diskParts,omitempty"`
	DiskFree       uint64   `json:"diskFree"`   // free on the filesystem the app lives on
	DiskPerDay     float64  `json:"diskPerDay"` // bytes a day (0: flat, shrinking or not enough data)
	DiskDaysLeft   float64  `json:"diskDaysLeft"`
	DiskFullAt     int64    `json:"diskFullAt"`
	DiskPoints     []Point  `json:"diskPoints"`     // MB
	DiskProjection []Point  `json:"diskProjection"` // MB, to DiskFullAt or 7 days ahead
}

type hourMin struct {
	t   int64
	mem float64
}

// hourlyFloor is each hour's lowest memory reading for app, oldest first.
func hourlyFloor(samples []Sample, app string) []hourMin {
	return hourly(samples, app, func(x AppSample) uint64 { return x.Mem })
}

// hourly is each hour's lowest non-zero value of v for app, oldest first.
func hourly(samples []Sample, app string, v func(AppSample) uint64) []hourMin {
	var out []hourMin
	for _, s := range samples {
		x, ok := s.Apps[app]
		if !ok || v(x) == 0 {
			continue
		}
		h := s.T / 3600 * 3600
		if n := len(out); n > 0 && out[n-1].t == h {
			out[n-1].mem = math.Min(out[n-1].mem, float64(v(x)))
		} else {
			out = append(out, hourMin{h, float64(v(x))})
		}
	}
	return out
}

// diskFree is the free space on the filesystem apps live on in sample s.
func diskFree(s Sample) uint64 {
	for _, d := range s.Disks {
		if d.Path == config.AppsDir {
			return d.Free
		}
	}
	if len(s.Disks) > 0 {
		return s.Disks[0].Free
	}
	return 0
}

// sinceRestart drops the hours before the last restart: a fall to under 60% of the previous hour's floor.
func sinceRestart(h []hourMin) []hourMin {
	for i := len(h) - 1; i > 0; i-- {
		if h[i].mem < 0.6*h[i-1].mem {
			return h[i:]
		}
	}
	return h
}

// slopePerDay fits a line through the floor; bytes a day, and false with under a day of data.
func slopePerDay(h []hourMin) (float64, bool) {
	if len(h) < 6 || h[len(h)-1].t-h[0].t < 86400 {
		return 0, false
	}
	var mx, my float64
	for _, p := range h {
		mx, my = mx+float64(p.t), my+p.mem
	}
	n := float64(len(h))
	mx, my = mx/n, my/n
	var num, den float64
	for _, p := range h {
		num += (float64(p.t) - mx) * (p.mem - my)
		den += (float64(p.t) - mx) * (float64(p.t) - mx)
	}
	if den == 0 {
		return 0, false
	}
	return num / den * 86400, true
}

func appTrends(all []Sample, now time.Time, span time.Duration) []AppTrend {
	if len(all) == 0 {
		return []AppTrend{}
	}
	last := all[len(all)-1]
	week := since(all, now, 7*24*time.Hour)
	day := since(all, now, 24*time.Hour)
	shown := since(all, now, span)
	var out []AppTrend
	for app, x := range last.Apps {
		t := AppTrend{App: app, Mem: x.Mem, Limit: x.MemMax, OwnLimit: x.MemMax > 0, DaysLeft: -1, Points: []Point{}, Projection: []Point{}}
		if !t.OwnLimit {
			t.Limit = last.MemTotal
		}
		if len(week) > 0 {
			if first, ok := week[0].Apps[app]; ok && x.OOM >= first.OOM {
				t.Kills = x.OOM - first.OOM
			}
		}
		if len(day) > 1 {
			a, b := day[0].Apps[app], day[len(day)-1].Apps[app]
			if secs := float64(day[len(day)-1].T - day[0].T); secs > 0 && b.CPUNs >= a.CPUNs {
				t.CPU = math.Round(float64(b.CPUNs-a.CPUNs)/1e9/secs*1000) / 10
			}
		}
		floor := sinceRestart(hourlyFloor(week, app))
		if slope, ok := slopePerDay(floor); ok && slope > 1<<20 { // at least 1 MB a day
			t.PerDay = slope
			if base := floor[len(floor)-1].mem; t.Limit > 0 && float64(t.Limit) > base {
				t.DaysLeft = math.Round((float64(t.Limit)-base)/slope*10) / 10
				t.FullAt = now.Add(time.Duration(t.DaysLeft * 24 * float64(time.Hour))).Unix()
				end := math.Min(t.DaysLeft, 7)
				t.Projection = []Point{{T: now.Unix(), V: toMB(base)}, {T: now.Add(time.Duration(end * 24 * float64(time.Hour))).Unix(), V: toMB(base + slope*end)}}
			}
		}
		t.Points = appPoints(shown, app, 120, func(x AppSample) uint64 { return x.Mem })
		t.CPUPoints = cpuPoints(shown, app, 120)
		diskTrend(&t, week, shown, last, x, now)
		out = append(out, t)
	}
	// soonest trouble first, then the biggest
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.DaysLeft >= 0) != (b.DaysLeft >= 0) {
			return a.DaysLeft >= 0
		}
		if a.DaysLeft >= 0 && a.DaysLeft != b.DaysLeft {
			return a.DaysLeft < b.DaysLeft
		}
		return a.Mem > b.Mem
	})
	return out
}

func toMB(b float64) float64 { return math.Round(b/(1<<20)*10) / 10 }

// diskTrend fills in t's disk fields: a straight line through the last week's hourly readings (disk doesn't
// restart like memory does), and when the free space runs out at that rate.
func diskTrend(t *AppTrend, week, shown []Sample, last Sample, x AppSample, now time.Time) {
	t.DiskDaysLeft, t.DiskPoints, t.DiskProjection = -1, []Point{}, []Point{}
	// the newest reading that has a disk figure: one taken before the first hourly pass has none
	for i := len(week) - 1; i >= 0 && x.Disk == 0; i-- {
		if week[i].T < now.Add(-2*time.Hour).Unix() {
			break
		}
		x = week[i].Apps[t.App]
	}
	if x.Disk == 0 {
		return
	}
	t.Disk, t.DiskFree = x.Disk, diskFree(last)
	if d, ok := lastAppDisk(t.App); ok {
		t.DiskParts = &d
		if d.At > last.T { // measured since the last sample (the dashboard asked): the newer figure
			t.Disk = d.Total
		}
	}
	t.DiskPoints = appPoints(shown, t.App, 120, func(x AppSample) uint64 { return x.Disk })
	h := hourly(week, t.App, func(x AppSample) uint64 { return x.Disk })
	slope, ok := slopePerDay(h)
	if !ok || slope <= 1<<20 { // under 1 MB a day: steady
		return
	}
	t.DiskPerDay = slope
	if t.DiskFree == 0 {
		return
	}
	base := float64(x.Disk)
	t.DiskDaysLeft = math.Round(float64(t.DiskFree)/slope*10) / 10
	t.DiskFullAt = now.Add(time.Duration(t.DiskDaysLeft * 24 * float64(time.Hour))).Unix()
	end := math.Min(t.DiskDaysLeft, 7)
	t.DiskProjection = []Point{{T: now.Unix(), V: toMB(base)}, {T: now.Add(time.Duration(end * 24 * float64(time.Hour))).Unix(), V: toMB(base + slope*end)}}
}

// appPoints averages one of app's figures (v) into at most n buckets, in MB.
func appPoints(in []Sample, app string, n int, v func(AppSample) uint64) []Point {
	if len(in) == 0 {
		return []Point{}
	}
	start, end := float64(in[0].T), float64(in[len(in)-1].T)
	step := math.Max((end-start)/float64(n), 300)
	type acc struct{ sum, n float64 }
	buckets := map[int]*acc{}
	for _, s := range in {
		x, ok := s.Apps[app]
		if !ok || v(x) == 0 {
			continue
		}
		i := min(int((float64(s.T)-start)/step), n-1)
		if buckets[i] == nil {
			buckets[i] = &acc{}
		}
		buckets[i].sum += float64(v(x))
		buckets[i].n++
	}
	keys := make([]int, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]Point, 0, len(keys))
	for _, k := range keys {
		out = append(out, Point{T: int64(start + (float64(k)+0.5)*step), V: toMB(buckets[k].sum / buckets[k].n)})
	}
	return out
}

// cpuPoints is app's CPU use between consecutive samples, as % of one core, averaged into at most n buckets.
// Gaps under a minute are skipped (too noisy), and so are falls in the running count (it restarted).
func cpuPoints(in []Sample, app string, n int) []Point {
	if len(in) < 2 {
		return []Point{}
	}
	start, end := float64(in[0].T), float64(in[len(in)-1].T)
	step := math.Max((end-start)/float64(n), 300)
	type acc struct{ sum, n float64 }
	buckets := map[int]*acc{}
	for i := 1; i < len(in); i++ {
		a, okA := in[i-1].Apps[app]
		b, okB := in[i].Apps[app]
		dt := float64(in[i].T - in[i-1].T)
		if !okA || !okB || dt < 60 || b.CPUNs < a.CPUNs || a.CPUNs == 0 {
			continue
		}
		k := min(int((float64(in[i].T)-start)/step), n-1)
		if buckets[k] == nil {
			buckets[k] = &acc{}
		}
		buckets[k].sum += float64(b.CPUNs-a.CPUNs) / 1e9 / dt * 100
		buckets[k].n++
	}
	keys := make([]int, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]Point, 0, len(keys))
	for _, k := range keys {
		out = append(out, Point{T: int64(start + (float64(k)+0.5)*step), V: math.Round(buckets[k].sum/buckets[k].n*10) / 10})
	}
	return out
}

// quotaPct is systemd's CPUQuotaPerSecUSec ("500ms", "1s", "1.5s", "infinity") as % of one core; 0 for no limit.
func quotaPct(q string) float64 {
	q = strings.TrimSpace(q)
	mul := 0.1 // ms
	if v, ok := strings.CutSuffix(q, "ms"); ok {
		q = v
	} else if v, ok := strings.CutSuffix(q, "s"); ok {
		q, mul = v, 100
	} else {
		return 0
	}
	n, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return 0
	}
	return math.Round(n*mul*10) / 10
}

// trendAdvice warns about an app heading for its limit (or the server's memory) within two weeks.
func trendAdvice(ts []AppTrend, _ time.Time) []Advice {
	var out []Advice
	for _, t := range ts {
		if t.DiskDaysLeft >= 0 && t.DiskDaysLeft <= 30 {
			level := "watch"
			if t.DiskDaysLeft <= 7 {
				level = "tight"
			}
			out = append(out, Advice{Level: level, App: t.App, Title: t.App + " is filling the disk",
				Detail: fmt.Sprintf("Up %s a day over the last week (%s now). At that rate the disk is full in %s. Look at what it keeps (its data, logs or old builds), or add storage.", sizeText(t.DiskPerDay), sizeText(float64(t.Disk)), humanDays(t.DiskDaysLeft))})
		}
		if t.DaysLeft < 0 || t.DaysLeft > 14 {
			continue
		}
		when := humanDays(t.DaysLeft)
		limit := "its " + mib(t.Limit) + " limit, and gets stopped"
		fix := "Find what keeps growing, restart it to buy time, or raise the limit on its Manage page."
		if !t.OwnLimit {
			limit = "all of the server's memory"
			fix = "Find what keeps growing, or give it a memory limit so it can't take the server down with it."
		}
		level := "watch"
		if t.DaysLeft <= 3 {
			level = "tight"
		}
		out = append(out, Advice{Level: level, App: t.App, Title: t.App + "'s memory keeps growing",
			Detail: fmt.Sprintf("Up %d MB a day since it last restarted (%s now). At that rate it reaches %s in %s. %s", int(math.Round(t.PerDay/(1<<20))), mib(t.Mem), limit, when, fix)})
	}
	return out
}

// sizeText is bytes as MB, or GB from 1 GB up.
func sizeText(b float64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", b/(1<<30))
	}
	if b < 1<<20 {
		return fmt.Sprintf("%d KB", int(math.Round(b/1024)))
	}
	return fmt.Sprintf("%d MB", int(math.Round(b/(1<<20))))
}
