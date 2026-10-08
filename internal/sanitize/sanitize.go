// Package sanitize removes what must not be persisted: identity values become stable pseudonyms,
// credentials are detected before anything is written, and free text keeps the mechanism while
// losing the project-specific instance.
package sanitize

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Redacted replaces a credential found in a field that is allowed to lose it.
const Redacted = "[REDACTED]"

// Key seals identity values with one installation-local salt.
type Key struct {
	salt []byte
}

const saltSize = 32

// TelemetryDir is the one place that knows where a config directory keeps its ledgers, its salt
// and its resolution map. Every caller derives from it, so no caller can disagree about the layout.
func TelemetryDir(configDir string) string {
	return filepath.Join(configDir, "telemetry")
}

// LoadKey reads or creates the installation-local telemetry salt.
func LoadKey(configDir string) (*Key, error) {
	return LoadKeyIn(TelemetryDir(configDir))
}

// LoadKeyIn reads or creates the telemetry salt inside the telemetry directory itself — the
// directory that holds a ledger — so every ledger in one directory shares one salt and the same
// value gets the same pseudonym in each of them.
func LoadKeyIn(telemetryDir string) (*Key, error) {
	if err := os.MkdirAll(telemetryDir, 0700); err != nil {
		return nil, fmt.Errorf("create telemetry directory: %w", err)
	}

	path := filepath.Join(telemetryDir, ".salt")
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		salt, err := os.ReadFile(path)
		if err == nil {
			return keyFromSalt(path, salt)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read telemetry salt: %w", err)
		}
		if err := createSalt(path, telemetryDir); err != nil {
			return nil, err
		}
		if attempt+1 < maxAttempts {
			time.Sleep(time.Millisecond)
		}
	}
	return nil, fmt.Errorf("read telemetry salt: exceeded %d attempts", maxAttempts)
}

func createSalt(path, telemetryDir string) error {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate telemetry salt: %w", err)
	}
	file, err := os.CreateTemp(telemetryDir, ".salt-*")
	if err != nil {
		return fmt.Errorf("create temporary telemetry salt: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("protect telemetry salt: %w", err)
	}
	if _, err := file.Write(salt); err != nil {
		return fmt.Errorf("write telemetry salt: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close telemetry salt: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil && !errors.Is(err, os.ErrExist) {
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("publish telemetry salt: %w", err)
		}
	}
	return nil
}

func keyFromSalt(path string, salt []byte) (*Key, error) {
	if len(salt) != saltSize {
		return nil, fmt.Errorf("invalid telemetry salt: want %d bytes, got %d", saltSize, len(salt))
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, fmt.Errorf("protect telemetry salt: %w", err)
	}
	return &Key{salt: append([]byte(nil), salt...)}, nil
}

// ID returns a stable pseudonym for one identity value. Without a salt it returns
// an unsealed value as a deliberate fail-closed answer carrying no identity correlation.
func (k *Key) ID(prefix, value string) string {
	if k == nil || len(k.salt) == 0 {
		return prefix + "-unsealed"
	}
	hash := sha256.New()
	_, _ = hash.Write(k.salt)
	_, _ = io.WriteString(hash, value)
	sum := hash.Sum(nil)
	return prefix + "-" + hex.EncodeToString(sum)[:12]
}

type pseudonymRecord struct {
	Pseudonym string `json:"pseudonym"`
	Value     string `json:"value"`
}

// Remember appends one pseudonym-to-value line to the local resolution map, unless the map already holds that
// exact pair: the Stop gate remembers the same repository and plan on every session stop, and appending each time
// grew the map without bound while every lookup scans it whole. A pseudonym names one value by construction, so a
// pair already present is the whole answer.
func (k *Key) Remember(telemetryDir, pseudonym, value string) error {
	if err := os.MkdirAll(telemetryDir, 0700); err != nil {
		return fmt.Errorf("create telemetry directory: %w", err)
	}
	if known, ok := Resolve(telemetryDir, pseudonym); ok && known == value {
		return nil
	}
	line, err := json.Marshal(pseudonymRecord{Pseudonym: pseudonym, Value: value})
	if err != nil {
		return fmt.Errorf("encode pseudonym: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(telemetryDir, ".pseudonyms.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open pseudonym map: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return fmt.Errorf("protect pseudonym map: %w", err)
	}
	line = append(line, '\n')
	if _, err := file.Write(line); err != nil {
		file.Close()
		return fmt.Errorf("write pseudonym map: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close pseudonym map: %w", err)
	}
	return nil
}

// Resolve answers the original value for a pseudonym from the local map.
func Resolve(telemetryDir, pseudonym string) (string, bool) {
	file, err := os.Open(filepath.Join(telemetryDir, ".pseudonyms.jsonl"))
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var record pseudonymRecord
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Pseudonym == pseudonym {
			return record.Value, true
		}
	}
	return "", false
}

type secretMatch struct {
	Secret
	end      int
	priority int
}

type secretDetector struct {
	kind string
	re   *regexp.Regexp
}

var secretDetectors = []secretDetector{
	{kind: "private_key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{kind: "github_token", re: regexp.MustCompile(`(?i:\b(?:ghp|ghs)_|\bgithub_pat_)[A-Za-z0-9_]+`)},
	{kind: "gitlab_token", re: regexp.MustCompile(`(?i:\bglpat-)[A-Za-z0-9_-]+`)},
	{kind: "aws_access_key_id", re: regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`)},
	{kind: "slack_token", re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]+`)},
	{kind: "google_api_key", re: regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}`)},
	{kind: "openai_key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{kind: "npm_token", re: regexp.MustCompile(`(?i:\bnpm_)[A-Za-z0-9]{36}`)},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{kind: "connection_string", re: regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^/\s:@]+:[^@\s]+@[^\s]+`)},
	{kind: "credential_userinfo", re: regexp.MustCompile(`\b[^\s@/:]+:[^\s@]{6,}@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?`)},
	{kind: "credential_assignment", re: regexp.MustCompile(`(?i)\b(?:password|passwd|passphrase|secret|token|api_key|apikey|access_key|client_secret|authorization)\b\s*[:=]\s*[^\s]{8,}`)},
	{kind: "authorization", re: regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+\S{16,}`)},
}

var entropyRunRe = regexp.MustCompile(`[A-Za-z0-9+/=_-]{24,}`)

// Secret is one credential-shaped run found in text.
type Secret struct {
	Kind  string
	Index int
}

// Scan reports every credential-shaped run in text, in source order.
func Scan(text string) []Secret {
	matches := scanMatches(text)
	secrets := make([]Secret, len(matches))
	for i, match := range matches {
		secrets[i] = match.Secret
	}
	return secrets
}

func scanMatches(text string) []secretMatch {
	matches := make([]secretMatch, 0)
	for priority, detector := range secretDetectors {
		for _, index := range detector.re.FindAllStringIndex(text, -1) {
			matches = append(matches, secretMatch{
				Secret:   Secret{Kind: detector.kind, Index: index[0]},
				end:      index[1],
				priority: priority,
			})
		}
	}
	for _, index := range entropyRunRe.FindAllStringIndex(text, -1) {
		if isHighEntropy(text[index[0]:index[1]]) {
			matches = append(matches, secretMatch{
				Secret:   Secret{Kind: "high_entropy", Index: index[0]},
				end:      index[1],
				priority: len(secretDetectors),
			})
		}
	}
	return nonOverlappingMatches(matches)
}

func nonOverlappingMatches(matches []secretMatch) []secretMatch {
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Index != matches[j].Index {
			return matches[i].Index < matches[j].Index
		}
		return matches[i].priority < matches[j].priority
	})
	chosen := make([]secretMatch, 0, len(matches))
	for _, match := range matches {
		if len(chosen) > 0 && match.Index < chosen[len(chosen)-1].end {
			continue
		}
		chosen = append(chosen, match)
	}
	return chosen
}

func isHighEntropy(value string) bool {
	counts := make(map[byte]int)
	hasDigit, hasLetter := false, false
	for i := 0; i < len(value); i++ {
		char := value[i]
		counts[char]++
		hasDigit = hasDigit || char >= '0' && char <= '9'
		hasLetter = hasLetter || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
	}
	if !hasDigit || !hasLetter {
		return false
	}
	entropy := 0.0
	for _, count := range counts {
		probability := float64(count) / float64(len(value))
		entropy -= probability * math.Log2(probability)
	}
	return entropy >= 3.5
}

// Generalize keeps mechanisms while removing project-specific instances.
func Generalize(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = fencedCodeRe.ReplaceAllString(text, "[code omitted]")
	text = stackTraceRe.ReplaceAllString(text, "[stack trace omitted]")
	text = replaceEndpoints(text)
	text = replaceURLs(text)
	text = emailRe.ReplaceAllString(text, "an email address")
	text = replaceSourcePaths(text)
	text = replaceIPs(text)
	text = hostRe.ReplaceAllString(text, "a host")
	text = uuidRe.ReplaceAllString(text, "an identifier")
	return lowercaseHexRe.ReplaceAllString(text, "an identifier")
}

var (
	fencedCodeRe   = regexp.MustCompile("(?s)(?:```.*?```|~~~.*?~~~)")
	stackTraceRe   = regexp.MustCompile(`(?m)(?:(?:^[ \t]+at [^\r\n]*|^\t/[^\r\n]*:[0-9]+ \+0x[0-9A-Fa-f]+)(?:\r?\n|$)){2,}`)
	endpointRe     = regexp.MustCompile(`(?i)\b(GET|POST|PUT|PATCH|DELETE)[ \t]+(/[^\s<>"']+)`)
	urlRe          = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
	emailRe        = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	sourcePathRe   = regexp.MustCompile(`(?i)(^|[^a-z0-9_])((?:/[a-z0-9._~+%-]+){2,}|(?:[a-z0-9._~+%-]+/)*[a-z0-9._~+%-]+\.(?:go|js|jsx|ts|tsx|py|rb|java|c|h|cc|cpp|cxx|rs|php|cs|swift|kt|kts|scala|sh|bash|zsh|sql|html|css|vue|svelte|m|mm|dart|ex|exs|erl|fs|fsx|asm|proto|tf)|(?:internal|cmd|pkg|src|lib|app|services|modules|tools|assets|skills|api|web|ui|backend|frontend)(?:/[a-z0-9._~+%-]+)+)(?:[?#][^\s<>"']*)?([^a-z0-9_]|$)`)
	ipCandidateRe  = regexp.MustCompile(`[0-9A-Fa-f:.]{2,}`)
	hostRe         = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}\b`)
	uuidRe         = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	lowercaseHexRe = regexp.MustCompile(`\b[a-f0-9]{7,40}\b`)
	absolutePathRe = regexp.MustCompile(`(^|[^a-zA-Z0-9_:])/[a-zA-Z0-9._~+%-]+(?:/[a-zA-Z0-9._~+%-]+)*`)
	pseudonymRe    = regexp.MustCompile(`^[a-z]{2,8}-[a-f0-9]{12}$`)
)

func replaceEndpoints(text string) string {
	return endpointRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := endpointRe.FindStringSubmatch(match)
		path, suffix := trimPunctuation(parts[2])
		if path == "" {
			return match
		}
		return "a " + parts[1] + " endpoint" + suffix
	})
}

func replaceURLs(text string) string {
	return urlRe.ReplaceAllStringFunc(text, func(match string) string {
		_, suffix := trimPunctuation(match)
		return "a URL" + suffix
	})
}

func replaceSourcePaths(text string) string {
	return sourcePathRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := sourcePathRe.FindStringSubmatch(match)
		return parts[1] + "a source file" + parts[3]
	})
}

func replaceIPs(text string) string {
	return ipCandidateRe.ReplaceAllStringFunc(text, func(match string) string {
		if net.ParseIP(match) != nil {
			return "an IP address"
		}
		return match
	})
}

func trimPunctuation(value string) (string, string) {
	end := len(value)
	for end > 0 && strings.ContainsRune(".,!?;", rune(value[end-1])) {
		end--
	}
	return value[:end], value[end:]
}

// Field applies secret handling and then generalizes one free-text field.
func Field(text string, required bool) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	matches := scanMatches(text)
	if required && len(matches) > 0 {
		return "", fmt.Errorf("detected %s in this value", matches[0].Kind)
	}
	if len(matches) > 0 {
		text = redactMatches(text, matches)
	}
	return Generalize(text), nil
}

func redactMatches(text string, matches []secretMatch) string {
	var result strings.Builder
	last := 0
	for _, match := range matches {
		result.WriteString(text[last:match.Index])
		result.WriteString(Redacted)
		last = match.end
	}
	result.WriteString(text[last:])
	return result.String()
}

// Verify reports the first reason a value is still unsafe to persist.
func Verify(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if pseudonymRe.MatchString(text) {
		return nil
	}
	if matches := Scan(text); len(matches) > 0 {
		return fmt.Errorf("unsafe value: detected %s", matches[0].Kind)
	}
	if absolutePathRe.MatchString(text) {
		return errors.New("unsafe value: absolute path")
	}
	if urlRe.MatchString(text) {
		return errors.New("unsafe value: URL")
	}
	if emailRe.MatchString(text) {
		return errors.New("unsafe value: email address")
	}
	if hostRe.MatchString(text) {
		return errors.New("unsafe value: host")
	}
	if uuidRe.MatchString(text) || lowercaseHexRe.MatchString(text) {
		return errors.New("unsafe value: identifier")
	}
	return nil
}
