package bench

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// recordedPiRun is a real Pi run, not a hand-made stream, captured with the free model:
//
//	pi -p 'Reply with exactly the two characters: ok' --mode json \
//	  --model opencode/muse-spark-1.3-contributor-free --no-session
const recordedPiRun = `{"type":"session","version":3,"id":"01a0900d-6be5-728b-b577-23b7713819d9","timestamp":"2026-09-11T10:39:53.830Z","cwd":"/home/alesierraalta/documents/projects/tpp"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"Reply with exactly the two characters: ok"}],"timestamp":1789123196835}}
{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"Reply with exactly the two characters: ok"}],"timestamp":1789123196835}}
{"type":"message_start","message":{"role":"assistant","content":[],"api":"openai-responses","provider":"opencode","model":"muse-spark-1.3-contributor-free","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"pending","timestamp":1789123200615}}
{"type":"message_update","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"assistantMessageEvent":{"type":"thinking_start","contentIndex":0}}
{"type":"message_update","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"assistantMessageEvent":{"type":"thinking_end","contentIndex":0,"content":""}}
{"type":"message_update","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"assistantMessageEvent":{"type":"text_start","contentIndex":1}}
{"type":"message_update","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"ok"}}
{"type":"message_update","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"assistantMessageEvent":{"type":"text_end","contentIndex":1,"content":"ok"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","thinkingSignature":"{\"id\":\"rs_6aa3da810bba5fe7cc4b4537:rs_01a0900d8a567660b0a2bfc0dc4585a2\",\"type\":\"reasoning\",\"status\":\"completed\",\"encrypted_content\":\"Q-PaDgF8bcse0tkr1IleYMsd0ecF09ERxXi_zdMLZCjPWFRTNe1IraXufKGAKvFqOYgPYHq6w6BcuqtawA5sEtW0icDrPu-Ufno91wMjyjVq_sCC4Ld2UGYG1c0RiGF_HP95IsyZvXT59Zbzg-E7hnWNM_dvYgeav6UwWT7O-Qhw4rK55Mo5N0PHTabkz_WXj3KM890mw_Su2H07mjgeY18fMNrFXMJSMB9aEkbntQ-tvFzQuPq_SN4mBh_hvvSLt0UTs9Wl7m0tQzLaS0dKdKukm0_9X6JoECpQ4hW_S-5_r3KnxnXkXV1y65b9uGfkUZivJu5a2BfHlsSkNGS4_GlaKC7bxBm0jt09B3W6NqxAdnZzx8xBlWCiqIKNNsRl8GUPfgtrzC_v94_pB7_R8-qjVhaXol7cC48IltiHPprtA8Nm81PU2soDTIuIX4Hs2ffnauFpT8CmRd4uwIkwT0fgDnaBzQPiq1IzgxjVVXJZu9CE1kcV49pXzGiOXQZf4xJbRxljBCUd1gpwZc2EjX1VVUfvNuzZc0hGqBiaRYCbaaOvm8tyTyGAZzyfyb9WjnzmzCIoAfR0RPUITNu7ic2fv0MLt6W9glAfcYdF2EvY0HUeDjmWvkZPyKaNx6FDkObmsrd3rz9Bhjo1IqKSVrJy6hQsh7tQZcjVLTG52PUBZo6hMcGnZXDiHsDvWj0qFB6LdaORGpNzeC3LHhEshG0sB2vXN_hM_u-6vjeu0BplBmqzJCVRhwGQVpZDdHT-D9Iu0HAXwjU-zVQlQJ1F8CDcvzO4TElW6lnjimtYCk_kq8lzmiUBTggFyC4A9TW39S0c6UL_894QxtnJ9EyW2Iz_qtH1tf3UWeycg8r_aDzJ1dHe7srBRROPnZlJFCSq4fOz5YAJmkzUaGGvVDTVJPeTvRdO2nWg6VCnibflVtEk--kye1TWNoVQDOTd89Z1bsqN-iI4tIxUxpjsC_tDntEpNdnINrB8yYrK6hmaJzthSe4PEX0tkQ5Y3JjFZJ9Hq9x9f_MTtuP8DdzYfmzsZx_vtTNaXU3itpQ_FrICNCaYTUTGDyBoQzDGbptgI3p-NxlOQI0mqLleywZwonF0BeM262yMz7Q4JHJUjTciIDfgWYfC3RZsc_YtQXsE1r7pWgPkTmzYvllCIBOSVmljSheN3XM0Pp2NH-aVhg6yV0TYBKtksRtpcQVhbOcLpzJlhhC19r9KJGOlFwZguTi63JO860Gl_ExDkzZ7jQDj_A2NcdIZz-hJ1jjuV36q1jUyfdAxAXJvaDj6-UIoOHC0q-BiZX1LegG624tlW7tySWrT86RGWQnAOrWNp8xXCBiOgBBc5H5GuT-BWuqRCZ1uQR0h-W6Ytikdv-gr2JrYxcVKs3WhFJ667CqRj03a7TE50Bh_v5UeePLTsQ7C-g\",\"summary\":[]}"},{"type":"text","text":"ok","textSignature":"{\"v\":1,\"id\":\"msg_01a0900d8b0c7ee3988bb792f0b27d61\"}"}],"api":"openai-responses","provider":"opencode","model":"muse-spark-1.3-contributor-free","usage":{"input":33254,"output":23,"cacheRead":113,"cacheWrite":0,"reasoning":12,"totalTokens":33390,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1789123200615,"responseId":"resp_6aa3da810bba5fe7cc4b4537","rawStopReason":"completed"}}
{"type":"turn_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","thinkingSignature":"{\"id\":\"rs_6aa3da810bba5fe7cc4b4537:rs_01a0900d8a567660b0a2bfc0dc4585a2\",\"type\":\"reasoning\",\"status\":\"completed\",\"encrypted_content\":\"Q-PaDgF8bcse0tkr1IleYMsd0ecF09ERxXi_zdMLZCjPWFRTNe1IraXufKGAKvFqOYgPYHq6w6BcuqtawA5sEtW0icDrPu-Ufno91wMjyjVq_sCC4Ld2UGYG1c0RiGF_HP95IsyZvXT59Zbzg-E7hnWNM_dvYgeav6UwWT7O-Qhw4rK55Mo5N0PHTabkz_WXj3KM890mw_Su2H07mjgeY18fMNrFXMJSMB9aEkbntQ-tvFzQuPq_SN4mBh_hvvSLt0UTs9Wl7m0tQzLaS0dKdKukm0_9X6JoECpQ4hW_S-5_r3KnxnXkXV1y65b9uGfkUZivJu5a2BfHlsSkNGS4_GlaKC7bxBm0jt09B3W6NqxAdnZzx8xBlWCiqIKNNsRl8GUPfgtrzC_v94_pB7_R8-qjVhaXol7cC48IltiHPprtA8Nm81PU2soDTIuIX4Hs2ffnauFpT8CmRd4uwIkwT0fgDnaBzQPiq1IzgxjVVXJZu9CE1kcV49pXzGiOXQZf4xJbRxljBCUd1gpwZc2EjX1VVUfvNuzZc0hGqBiaRYCbaaOvm8tyTyGAZzyfyb9WjnzmzCIoAfR0RPUITNu7ic2fv0MLt6W9glAfcYdF2EvY0HUeDjmWvkZPyKaNx6FDkObmsrd3rz9Bhjo1IqKSVrJy6hQsh7tQZcjVLTG52PUBZo6hMcGnZXDiHsDvWj0qFB6LdaORGpNzeC3LHhEshG0sB2vXN_hM_u-6vjeu0BplBmqzJCVRhwGQVpZDdHT-D9Iu0HAXwjU-zVQlQJ1F8CDcvzO4TElW6lnjimtYCk_kq8lzmiUBTggFyC4A9TW39S0c6UL_894QxtnJ9EyW2Iz_qtH1tf3UWeycg8r_aDzJ1dHe7srBRROPnZlJFCSq4fOz5YAJmkzUaGGvVDTVJPeTvRdO2nWg6VCnibflVtEk--kye1TWNoVQDOTd89Z1bsqN-iI4tIxUxpjsC_tDntEpNdnINrB8yYrK6hmaJzthSe4PEX0tkQ5Y3JjFZJ9Hq9x9f_MTtuP8DdzYfmzsZx_vtTNaXU3itpQ_FrICNCaYTUTGDyBoQzDGbptgI3p-NxlOQI0mqLleywZwonF0BeM262yMz7Q4JHJUjTciIDfgWYfC3RZsc_YtQXsE1r7pWgPkTmzYvllCIBOSVmljSheN3XM0Pp2NH-aVhg6yV0TYBKtksRtpcQVhbOcLpzJlhhC19r9KJGOlFwZguTi63JO860Gl_ExDkzZ7jQDj_A2NcdIZz-hJ1jjuV36q1jUyfdAxAXJvaDj6-UIoOHC0q-BiZX1LegG624tlW7tySWrT86RGWQnAOrWNp8xXCBiOgBBc5H5GuT-BWuqRCZ1uQR0h-W6Ytikdv-gr2JrYxcVKs3WhFJ667CqRj03a7TE50Bh_v5UeePLTsQ7C-g\",\"summary\":[]}"},{"type":"text","text":"ok","textSignature":"{\"v\":1,\"id\":\"msg_01a0900d8b0c7ee3988bb792f0b27d61\"}"}],"api":"openai-responses","provider":"opencode","model":"muse-spark-1.3-contributor-free","usage":{"input":33254,"output":23,"cacheRead":113,"cacheWrite":0,"reasoning":12,"totalTokens":33390,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1789123200615,"responseId":"resp_6aa3da810bba5fe7cc4b4537","rawStopReason":"completed"},"toolResults":[]}
{"type":"agent_end","messages":[{"role":"user","content":[{"type":"text","text":"Reply with exactly the two characters: ok"}],"timestamp":1789123196835},{"role":"assistant","content":[{"type":"thinking","thinking":"","thinkingSignature":"{\"id\":\"rs_6aa3da810bba5fe7cc4b4537:rs_01a0900d8a567660b0a2bfc0dc4585a2\",\"type\":\"reasoning\",\"status\":\"completed\",\"encrypted_content\":\"Q-PaDgF8bcse0tkr1IleYMsd0ecF09ERxXi_zdMLZCjPWFRTNe1IraXufKGAKvFqOYgPYHq6w6BcuqtawA5sEtW0icDrPu-Ufno91wMjyjVq_sCC4Ld2UGYG1c0RiGF_HP95IsyZvXT59Zbzg-E7hnWNM_dvYgeav6UwWT7O-Qhw4rK55Mo5N0PHTabkz_WXj3KM890mw_Su2H07mjgeY18fMNrFXMJSMB9aEkbntQ-tvFzQuPq_SN4mBh_hvvSLt0UTs9Wl7m0tQzLaS0dKdKukm0_9X6JoECpQ4hW_S-5_r3KnxnXkXV1y65b9uGfkUZivJu5a2BfHlsSkNGS4_GlaKC7bxBm0jt09B3W6NqxAdnZzx8xBlWCiqIKNNsRl8GUPfgtrzC_v94_pB7_R8-qjVhaXol7cC48IltiHPprtA8Nm81PU2soDTIuIX4Hs2ffnauFpT8CmRd4uwIkwT0fgDnaBzQPiq1IzgxjVVXJZu9CE1kcV49pXzGiOXQZf4xJbRxljBCUd1gpwZc2EjX1VVUfvNuzZc0hGqBiaRYCbaaOvm8tyTyGAZzyfyb9WjnzmzCIoAfR0RPUITNu7ic2fv0MLt6W9glAfcYdF2EvY0HUeDjmWvkZPyKaNx6FDkObmsrd3rz9Bhjo1IqKSVrJy6hQsh7tQZcjVLTG52PUBZo6hMcGnZXDiHsDvWj0qFB6LdaORGpNzeC3LHhEshG0sB2vXN_hM_u-6vjeu0BplBmqzJCVRhwGQVpZDdHT-D9Iu0HAXwjU-zVQlQJ1F8CDcvzO4TElW6lnjimtYCk_kq8lzmiUBTggFyC4A9TW39S0c6UL_894QxtnJ9EyW2Iz_qtH1tf3UWeycg8r_aDzJ1dHe7srBRROPnZlJFCSq4fOz5YAJmkzUaGGvVDTVJPeTvRdO2nWg6VCnibflVtEk--kye1TWNoVQDOTd89Z1bsqN-iI4tIxUxpjsC_tDntEpNdnINrB8yYrK6hmaJzthSe4PEX0tkQ5Y3JjFZJ9Hq9x9f_MTtuP8DdzYfmzsZx_vtTNaXU3itpQ_FrICNCaYTUTGDyBoQzDGbptgI3p-NxlOQI0mqLleywZwonF0BeM262yMz7Q4JHJUjTciIDfgWYfC3RZsc_YtQXsE1r7pWgPkTmzYvllCIBOSVmljSheN3XM0Pp2NH-aVhg6yV0TYBKtksRtpcQVhbOcLpzJlhhC19r9KJGOlFwZguTi63JO860Gl_ExDkzZ7jQDj_A2NcdIZz-hJ1jjuV36q1jUyfdAxAXJvaDj6-UIoOHC0q-BiZX1LegG624tlW7tySWrT86RGWQnAOrWNp8xXCBiOgBBc5H5GuT-BWuqRCZ1uQR0h-W6Ytikdv-gr2JrYxcVKs3WhFJ667CqRj03a7TE50Bh_v5UeePLTsQ7C-g\",\"summary\":[]}"},{"type":"text","text":"ok","textSignature":"{\"v\":1,\"id\":\"msg_01a0900d8b0c7ee3988bb792f0b27d61\"}"}],"api":"openai-responses","provider":"opencode","model":"muse-spark-1.3-contributor-free","usage":{"input":33254,"output":23,"cacheRead":113,"cacheWrite":0,"reasoning":12,"totalTokens":33390,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1789123200615,"responseId":"resp_6aa3da810bba5fe7cc4b4537","rawStopReason":"completed"}],"willRetry":false}
{"type":"agent_settled"}`

func TestPiStreamReadsARecordedRun(t *testing.T) {
	ar := ParsePiStream(strings.NewReader(recordedPiRun))
	if ar.SessionID != "01a0900d-6be5-728b-b577-23b7713819d9" {
		t.Fatalf("session id = %q", ar.SessionID)
	}
	if ar.Turns != 1 {
		t.Fatalf("turns = %d, want 1", ar.Turns)
	}
	if ar.Result != "ok" {
		t.Fatalf("result = %q, want the assistant's text", ar.Result)
	}
	if ar.IsError || ar.ErrorText != "" {
		t.Fatalf("a run that answered is not an error: %v %q", ar.IsError, ar.ErrorText)
	}
	// ExitCode and TimedOut are the process's, not the stream's; the parser must leave them alone.
	if ar.ExitCode != 0 || ar.TimedOut {
		t.Fatalf("the parser must not invent process state: exit %d, timed out %v", ar.ExitCode, ar.TimedOut)
	}
}

// The free model reports a zero cost, so the recorded run cannot prove the sum. Only the two usage
// shapes are written here; the fixture above is what proves the parser against the wire.
func TestPiStreamSumsTheCostOfEveryAssistantMessage(t *testing.T) {
	stream := `{"type":"session","version":3,"id":"s","cwd":"/w"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"one"}],"usage":{"cost":{"total":0.01}},"stopReason":"stop"}}
{"type":"turn_end","message":{"role":"assistant","content":[]},"toolResults":[]}
{"type":"turn_start"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"two"}],"usage":{"cost":{"total":0.02}},"stopReason":"stop"}}
{"type":"turn_end","message":{"role":"assistant","content":[]},"toolResults":[]}
{"type":"agent_end","messages":[]}
`
	ar := ParsePiStream(strings.NewReader(stream))
	if ar.CostUSD != 0.03 {
		t.Fatalf("cost = %v, want the sum of the assistant messages", ar.CostUSD)
	}
	if ar.Turns != 2 || ar.Result != "two" {
		t.Fatalf("turns = %d, result = %q", ar.Turns, ar.Result)
	}
}

// The documented Pi usage wire keys decide what the run may claim: a present zero is a measured
// zero, and a missing key is unknown — never a measured zero.
func TestPiStreamUsageKeysAndUnknownUsage(t *testing.T) {
	assistant := func(usage string) AgentResult {
		t.Helper()
		stream := `{"type":"session","version":3,"id":"s","cwd":"/w"}` + "\n" +
			`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"usage":` + usage + `,"stopReason":"stop"}}` + "\n"
		return ParsePiStream(strings.NewReader(stream))
	}
	full := assistant(`{"input":10,"output":20,"cacheRead":5,"cacheWrite":2,"totalTokens":37,"cost":{"total":0.25}}`)
	if !full.CostKnown || full.CostUSD != 0.25 || !full.TokensKnown || full.Tokens != 37 {
		t.Fatalf("documented usage keys not read: %+v", full)
	}
	parts := assistant(`{"input":7,"output":3,"cost":{"total":0}}`)
	if !parts.TokensKnown || parts.Tokens != 10 {
		t.Fatalf("part keys without totalTokens must sum: %+v", parts)
	}
	if !parts.CostKnown || parts.CostUSD != 0 {
		t.Fatalf("a present zero cost is a measured zero, not unknown: %+v", parts)
	}
	unknownCost := assistant(`{"input":7,"output":3}`)
	if unknownCost.CostKnown || unknownCost.CostUSD != 0 {
		t.Fatalf("a missing cost.total must stay unknown, not measured zero: %+v", unknownCost)
	}
	unknownTokens := assistant(`{"cost":{"total":0.01}}`)
	if unknownTokens.TokensKnown || unknownTokens.Tokens != 0 {
		t.Fatalf("missing token keys must stay unknown, not measured zero: %+v", unknownTokens)
	}
	noAssistant := ParsePiStream(strings.NewReader(`{"type":"session","version":3,"id":"s","cwd":"/w"}` + "\n"))
	if noAssistant.CostKnown || noAssistant.TokensKnown {
		t.Fatalf("a stream without an assistant message must be unknown: %+v", noAssistant)
	}
}

// Pi reports a failed model call as an assistant message whose stop reason is "error": its print
// mode writes that message's reason and exits non-zero. The free model does not fail on demand, so
// this is the one hand-written event, shaped like the recorded ones above.
func TestPiStreamTreatsAnErroredAssistantMessageAsAFailure(t *testing.T) {
	stream := `{"type":"session","version":3,"id":"s","cwd":"/w"}
{"type":"message_end","message":{"role":"assistant","content":[],"usage":{"cost":{"total":0}},"stopReason":"error","errorMessage":"401 invalid api key"}}
`
	ar := ParsePiStream(strings.NewReader(stream))
	if !ar.IsError {
		t.Fatal("an assistant message that stopped with an error is a failed run")
	}
	if !strings.Contains(ar.ErrorText, "401 invalid api key") {
		t.Fatalf("error text = %q", ar.ErrorText)
	}
}

// A runner that cannot start, or that exits non-zero, is an infrastructure failure: runOnce retries
// it and reports it as failed instead of scoring it as a run that found nothing.
func TestPiAgentReportsASpawnFailureAndANonZeroExit(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("PATH", t.TempDir()) // no pi anywhere
	ar, err := piAgent(context.Background(), ws, Key{}, Options{})
	if err == nil {
		t.Fatalf("a missing pi must be an error, got %+v", ar)
	}
	if !strings.Contains(err.Error(), "pi:") {
		t.Fatalf("the error must name the runner: %v", err)
	}

	// A pi that exits non-zero without an event stream: the exit code has to survive into the result.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\necho 'No API key found for the selected model.' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ar, err = piAgent(context.Background(), ws, Key{}, Options{})
	if err == nil {
		t.Fatalf("a non-zero exit must be an error, got %+v", ar)
	}
	if ar.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", ar.ExitCode)
	}
	// The CLI's own reason travels with the failure: a bare exit status would hide the authentication
	// error the operator needs to see.
	if !strings.Contains(err.Error(), "No API key found") {
		t.Fatalf("the error must carry the CLI's message: %v", err)
	}
}

// A timeout is not an error to retry: it marks the run TimedOut, exactly as the Claude path does.
func TestPiAgentMarksATimeout(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The fake pi is found first; the rest of PATH stays so the script's own commands resolve.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	ar, err := piAgent(ctx, t.TempDir(), Key{}, Options{})
	if err != nil {
		t.Fatalf("a timeout is reported through the result, not as an error: %v", err)
	}
	if !ar.TimedOut {
		t.Fatalf("a killed run must be marked: %+v", ar)
	}
}

// The runner reaches the run and selects the agent: Options.Agent wins when a test sets it, and
// otherwise the named runner's constructor is what Run spawns.
func TestRunPicksTheAgentOfTheSelectedRunner(t *testing.T) {
	if got := reflect.ValueOf(runnerAgent(RunnerPi)).Pointer(); got != reflect.ValueOf(piAgent).Pointer() {
		t.Fatal("RunnerPi must select piAgent")
	}
	for _, name := range []string{"", RunnerClaude} {
		if got := reflect.ValueOf(runnerAgent(name)).Pointer(); got != reflect.ValueOf(claudeAgent).Pointer() {
			t.Fatalf("%q must select claudeAgent", name)
		}
	}
	if !KnownRunner(RunnerPi) || !KnownRunner(RunnerClaude) || !KnownRunner("") || KnownRunner("gemini") {
		t.Fatal("the runners the bench can spawn are claude, pi, and the empty default")
	}

	// An injected agent wins: naming the pi runner in a test must not spawn a real pi.
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	spawned := false
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		spawned = true
		if opts.Runner != RunnerPi {
			t.Errorf("the runner must reach Options: %q", opts.Runner)
		}
		return AgentResult{Result: "done", Turns: 1}, nil
	}
	if _, code := Run(Options{
		CasesGlob: filepath.Join(filepath.Dir(caseDir), "case-*"), Model: "fake", Runner: RunnerPi, Runs: 1,
		Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), Agent: agent,
	}); code != 0 || !spawned {
		t.Fatalf("exit %d, agent spawned %v", code, spawned)
	}
}

// The runner decides the model, so a reading never depends on a remembered flag: Pi runs on the free
// model the readings use, and the Claude alternative runs on the cheapest one.
func TestDefaultModelFollowsTheRunner(t *testing.T) {
	if got := DefaultModel(RunnerPi); got != "opencode/muse-spark-1.3-contributor-free" {
		t.Fatalf("pi default model = %q", got)
	}
	if got := DefaultModel(RunnerClaude); got != "haiku" {
		t.Fatalf("claude default model = %q, want the cheapest alternative", got)
	}
}
