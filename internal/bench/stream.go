package bench

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// AgentResult is what one agent run produced, as read from Claude Code's stream-json output.
type AgentResult struct {
	Result      string  `json:"result"`
	CostUSD     float64 `json:"cost_usd"`
	CostKnown   bool    `json:"cost_known"`
	Tokens      int     `json:"-"`
	TokensKnown bool    `json:"-"`
	Turns       int     `json:"turns"`
	DurationMS  int64   `json:"duration_ms"`
	SessionID   string  `json:"session_id"`
	ExitCode    int     `json:"exit_code"`
	TimedOut    bool    `json:"timed_out"`
	IsError     bool    `json:"is_error"`
	ErrorText   string  `json:"error_text,omitempty"`
	Raw         string  `json:"-"` // tail of the raw stream, written to agent.log for diagnosis
}

// ParseStream keeps the last "result" event of a stream-json transcript; every other line is
// ignored, including lines that are not JSON, so a partial stream still yields what it has.
func ParseStream(r io.Reader) AgentResult {
	var out AgentResult
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type       string       `json:"type"`
			Result     string       `json:"result"`
			CostUSD    *float64     `json:"total_cost_usd"`
			Turns      int          `json:"num_turns"`
			DurationMS int64        `json:"duration_ms"`
			SessionID  string       `json:"session_id"`
			IsError    bool         `json:"is_error"`
			Subtype    string       `json:"subtype"`
			Usage      *claudeUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.SessionID != "" && out.SessionID == "" {
			out.SessionID = ev.SessionID
		}
		if ev.Type == "result" {
			out.Result, out.Turns, out.DurationMS = ev.Result, ev.Turns, ev.DurationMS
			out.CostUSD, out.CostKnown = 0, ev.CostUSD != nil
			if ev.CostUSD != nil {
				out.CostUSD = *ev.CostUSD
			}
			out.Tokens, out.TokensKnown = usageTokens(ev.Usage)
			// A result event can carry an error instead of an answer; the exit code alone does not say so.
			out.IsError = ev.IsError || strings.HasPrefix(ev.Subtype, "error")
			if out.IsError {
				out.ErrorText = strings.TrimSpace(ev.Subtype + " " + ev.Result)
			}
			if ev.SessionID != "" {
				out.SessionID = ev.SessionID
			}
		}
	}
	return out
}

type claudeUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	TotalTokens              *int `json:"total_tokens"`
}

func usageTokens(usage *claudeUsage) (int, bool) {
	if usage == nil {
		return 0, false
	}
	if usage.TotalTokens != nil {
		return *usage.TotalTokens, true
	}
	var total int
	known := false
	for _, part := range []*int{usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens} {
		if part != nil {
			total += *part
			known = true
		}
	}
	return total, known
}
