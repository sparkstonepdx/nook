package core

import (
	"math"
	"strings"
	"testing"
	"time"
)

const mib1 = 1 << 20

// days of samples every 5 minutes, ending at now
func history(now time.Time, days int, f func(age time.Duration, s *Sample)) []Sample {
	var out []Sample
	n := days * 288
	for i := 0; i < n; i++ {
		age := time.Duration(n-1-i) * 5 * time.Minute
		s := Sample{T: now.Add(-age).Unix(), MemTotal: 4 * gib, MemAvail: 2 * gib, CPUs: 2, Apps: map[string]AppSample{}}
		f(age, &s)
		out = append(out, s)
	}
	return out
}

func find(ts []AppTrend, app string) AppTrend {
	for _, t := range ts {
		if t.App == app {
			return t
		}
	}
	return AppTrend{}
}

func TestAppTrends(t *testing.T) {
	now := time.Now()
	all := history(now, 7, func(age time.Duration, s *Sample) {
		days := 7 - age.Hours()/24
		spike := 0.0
		if int(age.Hours())%24 == 12 { // an hour of heavy use each day: shouldn't tilt the trend
			spike = 60
		}
		// leaking 10 MB a day from 100 MB, 256 MB limit
		s.Apps["subduction"] = AppSample{Mem: uint64((100 + 10*days + spike) * mib1), MemMax: 256 * mib1, CPUNs: uint64(days * 86400 * 0.05 * 1e9)}
		// flat at 50 MB
		s.Apps["blog"] = AppSample{Mem: 50 * mib1, MemMax: 256 * mib1}
		// restarted 2 days ago: grew fast before, flat since
		before := age > 48*time.Hour
		m := 40.0
		if before {
			m = 100 + 30*days
		}
		s.Apps["wordbag"] = AppSample{Mem: uint64(m * mib1), MemMax: 512 * mib1}
		// no limit of its own: heading for the server's 4 GB
		s.Apps["hog"] = AppSample{Mem: uint64((2000 + 200*days) * mib1)}
	})
	ts := appTrends(all, now, 7*24*time.Hour)
	if ts[0].App != "subduction" && ts[0].App != "hog" {
		t.Fatalf("the soonest trouble should be first, got %s", ts[0].App)
	}

	sub := find(ts, "subduction")
	if math.Abs(sub.PerDay/mib1-10) > 1 {
		t.Errorf("subduction: %.1f MB a day, want 10 (spikes shouldn't tilt it)", sub.PerDay/mib1)
	}
	// floor now ~170 MB, 86 MB to go at 10 a day
	if sub.DaysLeft < 7.5 || sub.DaysLeft > 9.5 {
		t.Errorf("subduction: %.1f days left, want about 8.6", sub.DaysLeft)
	}
	if len(sub.Projection) != 2 || sub.Projection[1].V <= sub.Projection[0].V {
		t.Errorf("subduction projection: %v", sub.Projection)
	}
	if sub.CPU < 4.5 || sub.CPU > 5.5 {
		t.Errorf("subduction CPU %.1f%%, want 5", sub.CPU)
	}
	if len(sub.Points) == 0 || len(sub.Points) > 120 {
		t.Errorf("subduction: %d points", len(sub.Points))
	}
	if b := find(ts, "blog"); b.DaysLeft != -1 || b.PerDay != 0 {
		t.Errorf("blog is flat: %+v", b)
	}
	if w := find(ts, "wordbag"); w.DaysLeft != -1 {
		t.Errorf("wordbag is flat since its restart; the growth before shouldn't count: %.1f days", w.DaysLeft)
	}
	h := find(ts, "hog")
	if h.OwnLimit || h.Limit != 4*gib || h.DaysLeft < 0 {
		t.Errorf("hog has no limit, so the server's memory is: %+v", h)
	}

	adv := trendAdvice(ts, now)
	got := map[string]Advice{}
	for _, a := range adv {
		got[a.App] = a
	}
	if a, ok := got["subduction"]; !ok || a.Level != "watch" || !strings.Contains(a.Detail, "256 MB limit") {
		t.Errorf("subduction advice: %+v", a)
	}
	if a, ok := got["hog"]; !ok || !strings.Contains(a.Detail, "server's memory") {
		t.Errorf("hog advice: %+v", a)
	}
	if _, ok := got["blog"]; ok {
		t.Error("no advice for a flat app")
	}

	// and it reaches the status and the notification
	v := analyse(all, now)
	found := false
	for _, a := range v.Advice {
		found = found || a.App == "subduction" && strings.Contains(a.Title, "keeps growing")
	}
	if !found || v.Status == "ok" {
		t.Errorf("analyse should warn about subduction: %s %+v", v.Status, v.Advice)
	}
}

func TestAppTrendNeedsADay(t *testing.T) {
	now := time.Now()
	all := history(now, 1, func(age time.Duration, s *Sample) {
		s.Apps["new"] = AppSample{Mem: uint64((100 + 50*(24-age.Hours())/24) * mib1), MemMax: 256 * mib1}
	})
	all = all[len(all)-200:] // under a day
	if tr := appTrends(all, now, 24*time.Hour); tr[0].DaysLeft != -1 {
		t.Fatal("under a day of data shouldn't project")
	}
}

func TestCPUPoints(t *testing.T) {
	now := time.Now()
	var ns uint64
	all := history(now, 1, func(age time.Duration, s *Sample) {
		if age == 6*time.Hour {
			ns = 0 // restarted: the count starts over
		}
		ns += uint64(150 * time.Second) // 1.5 s of CPU per 5 minutes: 50% of a core
		s.Apps["sync"] = AppSample{Mem: 100 * mib1, CPUNs: ns}
	})
	ps := cpuPoints(all, "sync", 120)
	if len(ps) < 100 {
		t.Fatalf("got %d points", len(ps))
	}
	for _, p := range ps {
		if p.V != 50 {
			t.Fatalf("point %v: want 50%%", p)
		}
	}
	if tr := find(appTrends(all, now, 24*time.Hour), "sync"); len(tr.CPUPoints) != len(ps) {
		t.Fatalf("trend has %d cpu points", len(tr.CPUPoints))
	}
}

func TestQuotaPct(t *testing.T) {
	for q, want := range map[string]float64{"500ms": 50, "1s": 100, "1.5s": 150, "2s": 200, "infinity": 0, "": 0, "[not set]": 0} {
		if got := quotaPct(q); got != want {
			t.Errorf("quotaPct(%q) = %v, want %v", q, got, want)
		}
	}
}
