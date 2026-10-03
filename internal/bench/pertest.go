package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/hookcmd"
)

// perTestCommand rewrites a suite command so it reports one line per test: node's TAP reporter
// and go's -json stream. Other runners keep their command and are read as one whole-suite result.
// The suite is one field stored as one string, so it is split by the shared rule the hook command uses; a
// suite the splitter cannot read is reported instead of rewritten, for the same reason the whole-suite path
// refuses it: the words read so far are the head of a command nobody wrote.
func perTestCommand(suite string) (argv []string, parser func(string) map[string]bool, err error) {
	fields, err := hookcmd.ShellWords(suite)
	if err != nil {
		return nil, nil, err
	}
	if len(fields) == 0 {
		return nil, nil, nil
	}
	switch {
	case fields[0] == "node" && len(fields) > 1 && fields[1] == "--test":
		return append(fields[:2:2], append([]string{"--test-reporter=tap"}, fields[2:]...)...), parseTAP, nil
	case fields[0] == "go" && len(fields) > 1 && fields[1] == "test":
		return append(fields[:2:2], append([]string{"-json"}, fields[2:]...)...), parseGoJSON, nil
	}
	return fields, nil, nil
}

// runCapture runs argv in dir and returns the exit code with the complete combined output.
func runCapture(dir string, argv []string, timeout time.Duration) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return code, buf.String()
}

// perTest runs the suite and returns each test's outcome by name. A runner without a per-test
// format yields the single entry "suite". A suite the splitter cannot read is refused before it runs.
func perTest(dir, suite string, timeout time.Duration) (map[string]bool, int, string, error) {
	argv, parser, err := perTestCommand(suite)
	if err != nil {
		return nil, -1, "", err
	}
	if argv == nil {
		return nil, -1, "", nil
	}
	code, out := runCapture(dir, argv, timeout)
	if parser == nil {
		return map[string]bool{"suite": code == 0}, code, out, nil
	}
	tests := parser(out)
	if len(tests) == 0 {
		// A build failure or an empty run: the whole suite stands as one result.
		tests = map[string]bool{"suite": code == 0}
	}
	return tests, code, out, nil
}

var tapLine = regexp.MustCompile(`^( *)(ok|not ok) \d+ - (.*?)(?: # .*)?$`)

// parseTAP reads node's TAP output; nested subtests are named parent/child by indentation.
func parseTAP(out string) map[string]bool {
	res := map[string]bool{}
	type frame struct {
		indent int
		name   string
	}
	var pending []frame // subtests seen deeper than the line being read, until their parent line closes them
	for _, line := range strings.Split(out, "\n") {
		m := tapLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent, ok, name := len(m[1]), m[2] == "ok", strings.TrimSpace(m[3])
		// Children are printed before their parent: name them once the parent line arrives.
		var children []frame
		for len(pending) > 0 && pending[len(pending)-1].indent > indent {
			children = append(children, pending[len(pending)-1])
			pending = pending[:len(pending)-1]
		}
		for _, c := range children {
			passed := res[c.name]
			delete(res, c.name)
			res[name+"/"+c.name] = passed
		}
		res[name] = ok
		pending = append(pending, frame{indent, name})
	}
	return res
}

// parseGoJSON reads go test -json events; a test is named package.Test.
func parseGoJSON(out string) map[string]bool {
	res := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		var ev struct {
			Action, Package, Test string
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Test == "" {
			continue
		}
		switch ev.Action {
		case "pass":
			res[ev.Package+"."+ev.Test] = true
		case "fail":
			res[ev.Package+"."+ev.Test] = false
		}
	}
	return res
}
