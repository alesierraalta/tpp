package eval

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	EntityFinding = "finding"
	EntityIssue   = "issue"
	EntityCaseRun = "case_run"
	EntityRun     = "run"

	EventAdmit      = "admit"
	EventDecide     = "decide"
	EventDerive     = "derive"
	EventReopen     = "reopen"
	EventTransition = "transition"
)

// Event is one append-only state or adjudication record.
type Event struct {
	Seq           int             `json:"seq"`
	PrevHash      string          `json:"prev_hash"`
	Hash          string          `json:"hash"`
	Entity        string          `json:"entity"`
	ID            string          `json:"id"`
	Kind          string          `json:"event"`
	PreviousState string          `json:"previous_state"`
	NewState      string          `json:"new_state"`
	Reason        string          `json:"reason"`
	TS            string          `json:"timestamp"`
	Adjudicator   string          `json:"adjudicator"`
	Payload       json.RawMessage `json:"payload"`
}

// Log is an append-only hash-chained event history.
type Log struct {
	Events []Event `json:"events"`
}

// Append validates and adds one event to the log.
func (l *Log) Append(event Event) (Event, error) {
	if event.ID == "" {
		return Event{}, fmt.Errorf("event id is empty")
	}
	if !validEventKind(event.Kind) {
		return Event{}, fmt.Errorf("unknown event kind %q", event.Kind)
	}
	states := l.States()
	current, exists := states[event.Entity+":"+event.ID]
	if !exists {
		var ok bool
		current, ok = initialState(event.Entity)
		if !ok {
			return Event{}, fmt.Errorf("unknown event entity %q", event.Entity)
		}
	}
	if event.PreviousState != current {
		return Event{}, fmt.Errorf("%s %q previous state %q does not match current state %q", event.Entity, event.ID, event.PreviousState, current)
	}
	if event.PreviousState == event.NewState {
		if event.Kind != EventDecide {
			return Event{}, fmt.Errorf("state-preserving event must be a decide event")
		}
	} else {
		if isTerminalState(event.Entity, event.PreviousState) {
			if event.Kind != EventReopen {
				return Event{}, fmt.Errorf("terminal state %q can change only through reopen", event.PreviousState)
			}
			if strings.TrimSpace(event.Reason) == "" || strings.TrimSpace(event.Adjudicator) == "" {
				return Event{}, fmt.Errorf("reopen requires reason and adjudicator")
			}
		} else if event.Kind == EventReopen {
			return Event{}, fmt.Errorf("reopen requires a terminal previous state")
		}
		if !canTransition(event.Entity, event.PreviousState, event.NewState) {
			return Event{}, fmt.Errorf("illegal %s transition %q -> %q", event.Entity, event.PreviousState, event.NewState)
		}
	}

	event.Seq = len(l.Events) + 1
	event.PrevHash = ""
	if len(l.Events) > 0 {
		event.PrevHash = l.Events[len(l.Events)-1].Hash
	}
	event.Hash = ""
	hash, err := hashEvent(event)
	if err != nil {
		return Event{}, fmt.Errorf("hash event: %w", err)
	}
	event.Hash = hash
	l.Events = append(l.Events, event)
	return event, nil
}

// Verify checks event sequence numbers, transitions, and the complete hash chain.
func (l Log) Verify() error {
	var folded Log
	for index, event := range l.Events {
		expected, err := folded.Append(event)
		if err != nil {
			return fmt.Errorf("event %d: %w", index+1, err)
		}
		if event.Seq != expected.Seq {
			return fmt.Errorf("event %d has sequence %d, want %d", index+1, event.Seq, expected.Seq)
		}
		if event.PrevHash != expected.PrevHash {
			return fmt.Errorf("event %d has previous hash %q, want %q", index+1, event.PrevHash, expected.PrevHash)
		}
		if event.Hash != expected.Hash {
			return fmt.Errorf("event %d hash does not verify", index+1)
		}
	}
	return nil
}

// States folds the log into current states keyed by "entity:id".
func (l Log) States() map[string]string {
	states := make(map[string]string)
	for _, event := range l.Events {
		states[event.Entity+":"+event.ID] = event.NewState
	}
	return states
}

// ReadLog reads and verifies a newline-delimited event log.
func ReadLog(reader io.Reader) (Log, error) {
	var log Log
	buffered := bufio.NewReader(reader)
	line := 0
	for {
		data, err := buffered.ReadBytes('\n')
		if len(data) > 0 {
			line++
			text := strings.TrimSpace(string(data))
			if text != "" {
				var event Event
				if unmarshalErr := json.Unmarshal([]byte(text), &event); unmarshalErr != nil {
					return Log{}, fmt.Errorf("parse event line %d: %w", line, unmarshalErr)
				}
				log.Events = append(log.Events, event)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Log{}, fmt.Errorf("read event log: %w", err)
		}
	}
	if err := log.Verify(); err != nil {
		return Log{}, err
	}
	return log, nil
}

// WriteLog writes the log as one JSON event per line.
func (l Log) WriteLog(writer io.Writer) error {
	for index, event := range l.Events {
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal event %d: %w", index+1, err)
		}
		line := append(data, '\n')
		for len(line) > 0 {
			written, err := writer.Write(line)
			if err != nil {
				return fmt.Errorf("write event %d: %w", index+1, err)
			}
			if written == 0 {
				return fmt.Errorf("write event %d: %w", index+1, io.ErrShortWrite)
			}
			line = line[written:]
		}
	}
	return nil
}

func hashEvent(event Event) (string, error) {
	event.Hash = ""
	if len(event.Payload) > 0 {
		if !json.Valid(event.Payload) {
			return "", fmt.Errorf("payload is not valid JSON")
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(event.Payload)))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("canonicalize payload: %w", err)
		}
		payload, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("canonicalize payload: %w", err)
		}
		event.Payload = payload
	}
	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func initialState(entity string) (string, bool) {
	switch entity {
	case EntityFinding:
		return string(FindingRaw), true
	case EntityIssue:
		return string(IssuePending), true
	case EntityCaseRun:
		return string(CaseRunCreated), true
	case EntityRun:
		return string(RunCreated), true
	default:
		return "", false
	}
}

func validEventKind(kind string) bool {
	switch kind {
	case EventAdmit, EventDecide, EventDerive, EventReopen, EventTransition:
		return true
	default:
		return false
	}
}

func isTerminalState(entity, state string) bool {
	switch entity {
	case EntityFinding:
		return FindingState(state).IsTerminal()
	case EntityIssue:
		return IssueState(state).IsTerminal()
	case EntityCaseRun:
		return CaseRunState(state).IsTerminal()
	case EntityRun:
		return RunState(state).IsTerminal()
	default:
		return false
	}
}

func canTransition(entity, from, to string) bool {
	switch entity {
	case EntityFinding:
		return CanTransitionFinding(FindingState(from), FindingState(to))
	case EntityIssue:
		return CanTransitionIssue(IssueState(from), IssueState(to))
	case EntityCaseRun:
		return CanTransitionCaseRun(CaseRunState(from), CaseRunState(to))
	case EntityRun:
		return CanTransitionRun(RunState(from), RunState(to))
	default:
		return false
	}
}
