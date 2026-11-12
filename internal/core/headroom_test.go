package core

import (
	"strings"
	"testing"
	"time"
)

const gib = 1 << 30

// day makes 288 samples over the last 24 h with f filling in each one.
func day(now time.Time, f func(i int, s *Sample)) []Sample {
	var out []Sample
	for i := 0; i < 288; i++ {
		s := Sample{T: now.Add(-24*time.Hour + time.Duration(i)*5*time.Minute).Unix(), MemTotal: 4 * gib, MemAvail: 2 * gib, CPUs: 2,
			Disks: []DiskSample{{Path: "/", Total: 80 * gib, Free: 50 * gib, Inodes: 1000, IFree: 900}}, Apps: map[string]AppSample{}}
		f(i, &s)
		out = append(out, s)
	}
	return out
}

func TestHeadroomPlenty(t *testing.T) {
	now := time.Now()
	v := analyse(day(now, func(int, *Sample) {}), now)
	if v.Status != "ok" || len(v.Advice) != 0 || !strings.HasPrefix(v.Summary, "Plenty of headroom: memory 2 of 4 GB, disk 30 of 80 GB") {
		t.Fatalf("%s %q %+v", v.Status, v.Summary, v.Advice)
	}
}

func TestHeadroomMemoryTight(t *testing.T) {
	now := time.Now()
	v := analyse(day(now, func(i int, s *Sample) {
		if i > 200 {
			s.PSIMem, s.MemAvail = 18, gib/10
		}
	}), now)
	if v.Status != "tight" || v.Advice[0].Title != "Add RAM" || !strings.Contains(v.Summary, "add RAM") {
		t.Fatalf("%s %q %+v", v.Status, v.Summary, v.Advice)
	}
}

func TestHeadroomCPU(t *testing.T) {
	now := time.Now()
	v := analyse(day(now, func(i int, s *Sample) { s.PSICPU = 40 }), now)
	if v.Status != "tight" || v.Advice[0].Title != "Add CPU" {
		t.Fatalf("%s %+v", v.Status, v.Advice)
	}
	v = analyse(day(now, func(i int, s *Sample) {
		if i%5 == 0 {
			s.PSICPU = 12
		}
	}), now)
	if v.Status != "watch" {
		t.Fatalf("busy a fifth of the time: %s", v.Status)
	}
}

func TestHeadroomDiskFilling(t *testing.T) {
	now := time.Now()
	// 7 days, losing 2 GB a day from 20 GB free: full in about 10 days
	var all []Sample
	for i := 0; i < 7*288; i++ {
		ts := now.Add(-7*24*time.Hour + time.Duration(i)*5*time.Minute)
		free := uint64(34*gib) - uint64(float64(2*gib)*float64(i)/288)
		all = append(all, Sample{T: ts.Unix(), MemTotal: 4 * gib, MemAvail: 3 * gib, Disks: []DiskSample{{Path: "/", Total: 80 * gib, Free: free, Inodes: 10, IFree: 9}}})
	}
	v := analyse(all, now)
	d := v.Resources[2]
	if d.Status != "tight" || !strings.Contains(d.Detail, "full in about 10 days") || v.Advice[0].Title != "Add storage" {
		t.Fatalf("%+v %+v", d, v.Advice)
	}
}

func TestHeadroomAppLimit(t *testing.T) {
	now := time.Now()
	v := analyse(day(now, func(i int, s *Sample) {
		oom := uint64(0)
		if i > 100 {
			oom = 3
		}
		s.Apps["kinlog"] = AppSample{Mem: 250 << 20, MemMax: 256 << 20, OOM: oom}
		s.Apps["blog"] = AppSample{Mem: 240 << 20, MemMax: 256 << 20}
	}), now)
	if len(v.Advice) != 2 || v.Advice[0].Title != "blog is near its memory limit" || v.Advice[1].Title != "Raise kinlog's memory limit" || !strings.Contains(v.Advice[1].Detail, "3 times") {
		t.Fatalf("%+v", v.Advice)
	}
	if v.Status != "tight" {
		t.Fatalf("status %s", v.Status)
	}
}

func TestHeadroomSeries(t *testing.T) {
	now := time.Now()
	sr, since := series(day(now, func(i int, s *Sample) { s.PSICPU = float64(i % 10) }), 24*time.Hour, now)
	if len(sr) != 3 || len(sr[0].Points) == 0 || len(sr[0].Points) > 200 || since == 0 || sr[0].Points[0].V != 50 {
		t.Fatalf("%d %d %v", len(sr), len(sr[0].Points), sr[0].Points[:1])
	}
}
