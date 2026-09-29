# Bubbletea and teatest

Only for projects using `github.com/charmbracelet/bubbletea`. Test `Model.Update()` directly for state transitions; use `teatest` (`github.com/charmbracelet/x/exp/teatest`; verify import path) only for interactive flows, asserting the final model or rendered contract.

```go
func TestModelUpdateEnter(t *testing.T) {
	next, _ := NewModel().Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(Model).Screen; got != ScreenMainMenu {
		t.Fatalf("screen = %v, want %v", got, ScreenMainMenu)
	}
}
```

Golden output of rendered views follows the golden rules in `SKILL.md`: normalize, review the diff, rerun without `-update`.
