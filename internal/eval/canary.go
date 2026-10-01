package eval

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Leak reports a ground-truth marker or path exposed in one source line.
type Leak struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
}

var (
	keyPathLeak = regexp.MustCompile(`bench/cases/[^\s]*KEY\.json|(^|[\s"'/])KEY\.json`)
	fixPathLeak = regexp.MustCompile(`fix/(all|keep-[A-Za-z0-9]+)/`)
)

// ScanLeaks detects canary text and answer-key/fix paths in a line-oriented source.
func ScanLeaks(source string, r io.Reader, canary string) ([]Leak, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	var leaks []Leak
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if canary != "" {
			if index := strings.Index(line, canary); index >= 0 {
				leaks = append(leaks, Leak{Source: source, Kind: "canary", Line: lineNo, Excerpt: boundedExcerpt(line, index)})
			}
		}
		for _, match := range keyPathLeak.FindAllStringIndex(line, -1) {
			leaks = append(leaks, Leak{Source: source, Kind: "key-path", Line: lineNo, Excerpt: boundedExcerpt(line, match[0])})
		}
		for _, match := range fixPathLeak.FindAllStringIndex(line, -1) {
			leaks = append(leaks, Leak{Source: source, Kind: "fix-path", Line: lineNo, Excerpt: boundedExcerpt(line, match[0])})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", source, err)
	}
	return leaks, nil
}

func boundedExcerpt(line string, byteIndex int) string {
	runes := []rune(line)
	index := utf8.RuneCountInString(line[:byteIndex])
	start := index - 60
	if start < 0 {
		start = 0
	}
	end := start + 120
	if end > len(runes) {
		end = len(runes)
		start = end - 120
		if start < 0 {
			start = 0
		}
	}
	return string(runes[start:end])
}
