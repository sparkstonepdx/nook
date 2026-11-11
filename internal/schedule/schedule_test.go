package schedule

import "testing"

func TestOnCalendar(t *testing.T) {
	ok := map[string]string{
		"*/15 * * * *":       "*-*-* *:0/15:00",
		"0 3 * * *":          "*-*-* 3:0:00",
		"30 9 * * 1-5":       "Mon..Fri *-*-* 9:30:00",
		"0 0 1 */3 *":        "*-1/3-1 0:0:00",
		"5,35 */2 * * 0":     "Sun *-*-* 0/2:5,35:00",
		"@daily":             "daily",
		"hourly":             "hourly",
		"Mon *-*-* 09:00:00": "Mon *-*-* 09:00:00",
		"0 12 * * 7":         "Sun *-*-* 12:0:00",
	}
	for in, want := range ok {
		got, err := OnCalendar(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0 0 1 * 1", "61 * * * *", "* * * * */2", "daily\nExecStart=/bin/sh", "x; rm -rf /", "0 25 * * *"} {
		if got, err := OnCalendar(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}
