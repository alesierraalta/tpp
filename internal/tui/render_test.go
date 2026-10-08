package tui

import (
	"errors"
	"testing"
)

func TestRenderMenu(t *testing.T) {
	want := "tsp tui\n" +
		"\n" +
		"  Status\n" +
		"  Features\n" +
		"> Sync plan\n" +
		"  Quit\n" +
		"\n" +
		"up/down move | enter select | q quit"
	if got := renderMenu(2); got != want {
		t.Errorf("renderMenu(2) =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderStatus(t *testing.T) {
	fixture := StatusView{
		StateRoot:        "/home/u/.config/tpp",
		StateExists:      true,
		InstalledVersion: "1.4.0",
		AvailableVersion: "unknown (no update check yet)",
		Features: []FeatureRow{
			{ID: "feedback", Title: "Feedback", Enabled: false},
		},
	}
	want := "tsp tui: status\n" +
		"\n" +
		"State root: /home/u/.config/tpp\n" +
		"State exists: yes\n" +
		"Installed version: 1.4.0\n" +
		"Available version: unknown (no update check yet)\n" +
		"\n" +
		"ID        Title     State\n" +
		"feedback  Feedback  disabled\n" +
		"\n" +
		"up/down scroll | pgup/pgdn page | esc back | q quit"
	if got := renderStatus(fixture, nil); got != want {
		t.Errorf("renderStatus ok =\n%q\nwant\n%q", got, want)
	}

	wantErr := "tsp tui: status\n" +
		"\n" +
		"error: status unavailable\n" +
		"\n" +
		"up/down scroll | pgup/pgdn page | esc back | q quit"
	if got := renderStatus(StatusView{}, errors.New("status unavailable")); got != wantErr {
		t.Errorf("renderStatus err =\n%q\nwant\n%q", got, wantErr)
	}
}

func TestRenderFeatures(t *testing.T) {
	rows := []FeatureRow{
		{ID: "feedback", Title: "Feedback", Enabled: false},
		{ID: "logging", Title: "Logging", Enabled: true},
	}
	want := "tsp tui: features\n" +
		"\n" +
		"> feedback  Feedback  disabled\n" +
		"  logging   Logging   enabled\n" +
		"\n" +
		"space toggle | enter preview | esc back | q quit"
	if got := renderFeatures(rows, 0, ""); got != want {
		t.Errorf("renderFeatures no detail =\n%q\nwant\n%q", got, want)
	}

	wantDetail := "tsp tui: features\n" +
		"\n" +
		"  feedback  Feedback  disabled\n" +
		"> logging   Logging   enabled\n" +
		"\n" +
		"Preview text.\n" +
		"\n" +
		"space toggle | enter preview | esc back | q quit"
	if got := renderFeatures(rows, 1, "Preview text.\n"); got != wantDetail {
		t.Errorf("renderFeatures with detail =\n%q\nwant\n%q", got, wantDetail)
	}
}

func TestRenderPlan(t *testing.T) {
	want := "tsp tui: sync plan (dry-run; writes nothing)\n" +
		"\n" +
		"line1\n" +
		"line2\n" +
		"\n" +
		"up/down scroll | pgup/pgdn page | esc back | q quit"
	if got := renderPlan("line1\nline2\n", nil); got != want {
		t.Errorf("renderPlan ok =\n%q\nwant\n%q", got, want)
	}

	wantErr := "tsp tui: sync plan (dry-run; writes nothing)\n" +
		"\n" +
		"error: plan failed\n" +
		"\n" +
		"up/down scroll | pgup/pgdn page | esc back | q quit"
	if got := renderPlan("", errors.New("plan failed")); got != wantErr {
		t.Errorf("renderPlan err =\n%q\nwant\n%q", got, wantErr)
	}
}
