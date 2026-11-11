package core

import "testing"

func TestCheckServices(t *testing.T) {
	ok := []Manifest{
		{Kind: "web", Needs: []string{"postgres", "alerts"}},
		{Kind: "service", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"ALERT_URL": "{url}/alert", "ALERT_TOKEN": "{token}"}}}},
		{Kind: "service", Provides: map[string]ProvideSpec{"my-queue": {Env: map[string]string{"QUEUE_URL": "http://{host}:{port}/q?key={token}"}}}},
	}
	for _, m := range ok {
		if err := checkServices(&m); err != nil {
			t.Errorf("%+v: %v", m, err)
		}
	}
	bad := []Manifest{
		{Kind: "web", Needs: []string{"Postgres!"}},
		{Kind: "web", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"ALERT_URL": "x", "ALERT_TOKEN": "y"}}}}, // only services provide
		{Kind: "service", Provides: map[string]ProvideSpec{"postgres": {Env: map[string]string{"DATABASE_URL": "x"}}}},            // built-in name
		{Kind: "service", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"HOOK": "{url}"}}}},                  // known interface, wrong names
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"PORT": "{port}"}}}},                      // reserved name
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"X_URL": "a\nExecStart=/bin/sh"}}}},       // newline
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"X_URL": "$(id)"}}}},                      // shell
	}
	for _, m := range bad {
		if err := checkServices(&m); err == nil {
			t.Errorf("accepted %+v", m)
		}
	}
}
