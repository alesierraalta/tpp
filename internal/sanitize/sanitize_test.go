package sanitize

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScanFindsEveryCredentialFamilyItClaims(t *testing.T) {
	cases := []struct {
		name string
		text string
		kind string
	}{
		{"private key", "-----BEGIN RSA PRIVATE KEY-----", "private_key"},
		{"github token", "ghp_1234567890abcdef1234", "github_token"},
		{"github token uppercase prefix", "GHP_1234567890abcdef1234", "github_token"},
		{"gitlab token", "glpat-1234567890abcdef1234567890", "gitlab_token"},
		{"gitlab token uppercase prefix", "GlPat-1234567890abcdef1234567890", "gitlab_token"},
		{"aws access key id", "AKIAIOSFODNN7EXAMPLE", "aws_access_key_id"},
		{"slack token", "xoxb-123456789012-123456789012", "slack_token"},
		{"google api key", "AIza" + strings.Repeat("A", 35), "google_api_key"},
		{"openai key", "sk-" + strings.Repeat("a1", 12), "openai_key"},
		{"npm token", "npm_" + strings.Repeat("a1", 18), "npm_token"},
		{"npm token uppercase prefix", "NPM_" + strings.Repeat("a1", 18), "npm_token"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature", "jwt"},
		{"jwt with empty first segment payload", "eyJ.eyJzdWIiOiIxMjMifQ.signature", "jwt"},
		{"connection string", "postgres://user:password@example.com/db", "connection_string"},
		{"credential assignment", "password=supersecret", "credential_assignment"},
		{"authorization", "Bearer " + strings.Repeat("a1", 8), "authorization"},
		{"high entropy run", "aA1bB2cC3dD4eE5fF6gG7hH8", "high_entropy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan(tc.text)
			if len(got) != 1 || got[0].Kind != tc.kind || got[0].Index != 0 {
				t.Fatalf("scan(%q) = %v, want one %s secret at index 0", tc.text, got, tc.kind)
			}
		})
	}
}

func TestScanFindsAnInlineCredentialWithoutAScheme(t *testing.T) {
	cases := []struct {
		text string
		kind string
	}{
		{"user:password@db", "credential_userinfo"},
		{"postgres://user:pass@host/db", "connection_string"},
		{"app:sup3rsecret@cache.internal", "credential_userinfo"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := Scan(tc.text)
			if len(got) != 1 || got[0].Kind != tc.kind || got[0].Index != 0 {
				t.Fatalf("scan(%q) = %v, want one %s secret at index 0", tc.text, got, tc.kind)
			}
		})
	}
}

func TestScanLeavesAShortUserInfoAlone(t *testing.T) {
	for _, value := range []string{"a:bcd@c", "a:b@c", "12:30@noon"} {
		if got := Scan(value); len(got) != 0 {
			t.Fatalf("scan(%q) = %v, want no secrets", value, got)
		}
	}
}

func TestScanLeavesAnEmailAddressAlone(t *testing.T) {
	for _, value := range []string{"someone@example.com", "first.last@sub.example.org"} {
		if got := Scan(value); len(got) != 0 {
			t.Fatalf("scan(%q) = %v, want no secrets", value, got)
		}
	}
}

func TestScanLeavesOrdinaryProseAlone(t *testing.T) {
	cases := []string{
		"tokenizer",
		"keyboard",
		"the secret sauce",
		"password policy",
		"key: the design is fine",
		"The team reviews ordinary telemetry notes carefully because clear explanations help operators understand behavior without exposing project-specific details during routine maintenance.",
	}
	for _, in := range cases {
		if got := Scan(in); len(got) != 0 {
			t.Fatalf("scan(%q) = %v, want no secrets", in, got)
		}
	}
}

func TestGeneralizeKeepsTheMechanismAndDropsTheInstance(t *testing.T) {
	in := strings.Join([]string{
		"POST /api/internal/users/{userId} failed in /home/alice/project/handler.go for alice@example.com.",
		"The request id was 123e4567-e89b-12d3-a456-426614174000.",
		"panic: failed\n\tat /home/alice/project/handler.go:12 +0x123\n\tat /home/alice/project/handler.go:13 +0x456",
		"```go\nfmt.Println(\"private\")\n```",
	}, "\n")
	got := Generalize(in)
	for _, unwanted := range []string{"/api/internal", "userId", "/home/alice", "alice@example.com", "123e4567-e89b-12d3-a456-426614174000", "```"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generalize(%q) = %q, still contains %q", in, got, unwanted)
		}
	}
	for _, wanted := range []string{"a POST endpoint", "a source file", "an email address", "an identifier", "[stack trace omitted]", "[code omitted]"} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("generalize(%q) = %q, want %q", in, got, wanted)
		}
	}
}

func TestGeneralizeDropsAQueryStringWithThePath(t *testing.T) {
	in := "post /api/x?tenant=alice failed in the ledger."
	got := Generalize(in)
	for _, unwanted := range []string{"/api/x", "tenant=alice"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generalize(%q) = %q, still contains %q", in, got, unwanted)
		}
	}
	if !strings.Contains(got, "a post endpoint failed") {
		t.Fatalf("generalize(%q) = %q, want the lowercase endpoint mechanism preserved", in, got)
	}
}

func TestGeneralizeDropsARelativeSourcePath(t *testing.T) {
	for _, path := range []string{"internal/tools/runner", "cmd/tpp/main", "src/app/models"} {
		in := "The failure happened in " + path + " during startup."
		got := Generalize(in)
		if strings.Contains(got, path) || !strings.Contains(got, "a source file") {
			t.Fatalf("generalize(%q) = %q, want the relative source path replaced", in, got)
		}
	}
}

func TestGeneralizeLeavesSlashSeparatedProseAlone(t *testing.T) {
	for _, value := range []string{"and/or", "input/output", "read/write"} {
		if got := Generalize(value); got != value {
			t.Fatalf("generalize(%q) = %q, want unchanged prose", value, got)
		}
	}
}

func TestGeneralizeIsIdempotent(t *testing.T) {
	in := "POST /api/internal/users/123 for alice@example.com at 192.168.1.42 in src/handler.go, commit deadbeef."
	first := Generalize(in)
	if second := Generalize(first); second != first {
		t.Fatalf("generalize is not idempotent: first %q, second %q", first, second)
	}
}

func TestFieldRefusesASecretInARequiredField(t *testing.T) {
	got, err := Field("notes contain ghp_1234567890abcdef1234", true)
	if err == nil || !strings.Contains(err.Error(), "github_token") {
		t.Fatalf("field required error = %v, want github_token detection", err)
	}
	if got != "" {
		t.Fatalf("field required value = %q, want empty", got)
	}
}

func TestFieldRedactsASecretInAnOptionalField(t *testing.T) {
	got, err := Field("notes contain ghp_1234567890abcdef1234", false)
	if err != nil {
		t.Fatalf("field optional error = %v", err)
	}
	want := "notes contain " + Redacted
	if got != want {
		t.Fatalf("field optional value = %q, want %q", got, want)
	}
	if err := Verify(got); err != nil {
		t.Fatalf("verify(%q) = %v after optional field sanitization", got, err)
	}
}

func TestVerifyAcceptsEveryPlaceholderThePackageProduces(t *testing.T) {
	placeholders := []string{
		Redacted,
		"a source file",
		"a URL",
		"a host",
		"an IP address",
		"an email address",
		"a GET endpoint",
		"a POST endpoint",
		"a PUT endpoint",
		"a PATCH endpoint",
		"a DELETE endpoint",
		"an identifier",
		"[code omitted]",
		"[stack trace omitted]",
		"",
	}
	for _, value := range placeholders {
		if err := Verify(value); err != nil {
			t.Fatalf("verify(%q) = %v, want placeholder accepted", value, err)
		}
	}
}

func TestVerifyAcceptsPseudonyms(t *testing.T) {
	if err := Verify("user-6b8e88df8866"); err != nil {
		t.Fatalf("verify pseudonym = %v, want nil", err)
	}
}

func TestIDRefusesToSealWithoutAKey(t *testing.T) {
	var nilKey *Key
	for _, key := range []*Key{nilKey, &Key{}} {
		if got := key.ID("repo", "public-repository"); got != "repo-unsealed" {
			t.Fatalf("ID without salt = %q, want repo-unsealed", got)
		}
		if got := key.ID("repo", "different-repository"); got != "repo-unsealed" {
			t.Fatalf("ID without salt for another value = %q, want stable repo-unsealed", got)
		}
	}
}

func TestIDIsStableUnderOneKeyAndDifferentUnderAnother(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	first, err := LoadKey(firstDir)
	if err != nil {
		t.Fatalf("load first key: %v", err)
	}
	second, err := LoadKey(secondDir)
	if err != nil {
		t.Fatalf("load second key: %v", err)
	}
	one := first.ID("user", "alice@example.com")
	if got := first.ID("user", "alice@example.com"); got != one {
		t.Fatalf("same key ID changed: first %q, second %q", one, got)
	}
	if got := second.ID("user", "alice@example.com"); got == one {
		t.Fatalf("different config directories produced the same pseudonym %q", got)
	}
	if !strings.HasPrefix(one, "user-") || len(one) != len("user-")+12 {
		t.Fatalf("ID = %q, want prefix and 12 hex characters", one)
	}
}

func TestOneTelemetryDirectoryHoldsOneSaltAndOneMap(t *testing.T) {
	configDir := t.TempDir()
	telemetryDir := TelemetryDir(configDir)
	fromConfig, err := LoadKey(configDir)
	if err != nil {
		t.Fatalf("load key from config dir: %v", err)
	}
	fromTelemetry, err := LoadKeyIn(telemetryDir)
	if err != nil {
		t.Fatalf("load key from telemetry dir: %v", err)
	}
	pseudonym := fromConfig.ID("repo", "same-value")
	if got := fromTelemetry.ID("repo", "same-value"); got != pseudonym {
		t.Fatalf("LoadKeyIn pseudonym = %q, LoadKey pseudonym = %q", got, pseudonym)
	}
	if err := fromTelemetry.Remember(telemetryDir, pseudonym, "same-value"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got, ok := Resolve(telemetryDir, pseudonym); !ok || got != "same-value" {
		t.Fatalf("resolve(%q) = %q, %t; want original value", pseudonym, got, ok)
	}
	for _, name := range []string{".salt", ".pseudonyms.jsonl"} {
		if _, err := os.Stat(filepath.Join(telemetryDir, name)); err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	nestedDir := filepath.Join(telemetryDir, filepath.Base(TelemetryDir("")))
	if _, err := os.Stat(nestedDir); !os.IsNotExist(err) {
		t.Fatalf("nested telemetry directory = %v, want absent", err)
	}
}

func TestLoadKeySurvivesConcurrentFirstUse(t *testing.T) {
	dir := t.TempDir()
	const workers = 8
	type result struct {
		key *Key
		err error
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func() {
			key, err := LoadKey(dir)
			results <- result{key: key, err: err}
		}()
	}

	var want []byte
	for i := 0; i < workers; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent load key: %v", got.err)
		}
		if got.key == nil || len(got.key.salt) == 0 {
			t.Fatal("concurrent load key returned an empty key")
		}
		if want == nil {
			want = append([]byte(nil), got.key.salt...)
		} else if !reflect.DeepEqual(got.key.salt, want) {
			t.Fatalf("concurrent loads returned different salts: %x and %x", got.key.salt, want)
		}
	}
	if len(want) == 0 {
		t.Fatal("concurrent loads returned no salt")
	}

	raw, err := os.ReadFile(filepath.Join(TelemetryDir(dir), ".salt"))
	if err != nil {
		t.Fatalf("read published salt: %v", err)
	}
	if len(raw) != saltSize {
		t.Fatalf("published salt length = %d, want %d", len(raw), saltSize)
	}
}

func TestLoadKeyReturnsWhenAStaleLockDirectoryIsPresent(t *testing.T) {
	dir := t.TempDir()
	telemetryDir := TelemetryDir(dir)
	if err := os.MkdirAll(telemetryDir, 0700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(telemetryDir, ".salt.lock"), 0700); err != nil {
		t.Fatalf("create stale lock directory: %v", err)
	}

	result := make(chan struct {
		key *Key
		err error
	}, 1)
	go func() {
		key, err := LoadKey(dir)
		result <- struct {
			key *Key
			err error
		}{key: key, err: err}
	}()

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("load key with stale lock: %v", got.err)
		}
		if got.key == nil || len(got.key.salt) != saltSize {
			t.Fatalf("load key with stale lock returned invalid key: %#v", got.key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load key with stale lock did not return")
	}
}

func TestLoadKeyIgnoresAStrayTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	telemetryDir := TelemetryDir(dir)
	if err := os.MkdirAll(telemetryDir, 0700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(telemetryDir, ".salt-abandoned"), []byte("garbage"), 0600); err != nil {
		t.Fatalf("write stray temporary file: %v", err)
	}

	key, err := LoadKey(dir)
	if err != nil {
		t.Fatalf("load key with stray temporary file: %v", err)
	}
	if key == nil || len(key.salt) != saltSize {
		t.Fatalf("load key with stray temporary file returned invalid key: %#v", key)
	}
}

func TestLoadKeyIsIdempotentAndPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadKey(dir)
	if err != nil {
		t.Fatalf("first load key: %v", err)
	}
	second, err := LoadKey(dir)
	if err != nil {
		t.Fatalf("second load key: %v", err)
	}
	if !reflect.DeepEqual(first.salt, second.salt) {
		t.Fatalf("salt changed between loads: %x and %x", first.salt, second.salt)
	}
	info, err := os.Stat(filepath.Join(TelemetryDir(dir), ".salt"))
	if err != nil {
		t.Fatalf("stat salt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("salt mode = %o, want 600", got)
	}
}

func TestRememberAndResolveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	telemetryDir := TelemetryDir(dir)
	key, err := LoadKey(dir)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	pseudonym := key.ID("user", "alice@example.com")
	if err := key.Remember(telemetryDir, pseudonym, "alice@example.com"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got, ok := Resolve(telemetryDir, pseudonym); !ok || got != "alice@example.com" {
		t.Fatalf("resolve(%q) = %q, %t, want original value", pseudonym, got, ok)
	}
	if got, ok := Resolve(telemetryDir, "user-unknown"); ok || got != "" {
		t.Fatalf("resolve unknown = %q, %t, want empty false", got, ok)
	}
	if got, ok := Resolve(TelemetryDir(t.TempDir()), pseudonym); ok || got != "" {
		t.Fatalf("resolve missing map = %q, %t, want empty false", got, ok)
	}
}

// A pair the map already holds is not written again: the Stop gate remembers the same repository and plan on
// every session stop, and a map that appends each time grows without bound. A new pair is still appended, and a
// torn line (a crash mid-write) neither breaks the lookup nor hides a pair recorded after it.
func TestRememberWritesAPairOnce(t *testing.T) {
	dir := t.TempDir()
	telemetryDir := TelemetryDir(dir)
	key, err := LoadKey(dir)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	mapPath := filepath.Join(telemetryDir, ".pseudonyms.jsonl")
	lineCount := func() int {
		raw, err := os.ReadFile(mapPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "\n")
	}
	repo := key.ID("repo", "alterCEO")
	for i := 0; i < 3; i++ {
		if err := key.Remember(telemetryDir, repo, "alterCEO"); err != nil {
			t.Fatalf("remember: %v", err)
		}
	}
	if n := lineCount(); n != 1 {
		t.Fatalf("map holds %d lines after remembering one pair three times, want 1", n)
	}

	// A torn line, then a new pair, then the old pair again.
	f, err := os.OpenFile(mapPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(make([]byte, 40), '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()
	plan := key.ID("plan", "docs/testing/test-plan.md")
	if err := key.Remember(telemetryDir, plan, "docs/testing/test-plan.md"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := key.Remember(telemetryDir, plan, "docs/testing/test-plan.md"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := key.Remember(telemetryDir, repo, "alterCEO"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if n := lineCount(); n != 3 {
		t.Fatalf("map holds %d lines, want 3 (one pair, the torn line, a second pair)", n)
	}
	for p, want := range map[string]string{repo: "alterCEO", plan: "docs/testing/test-plan.md"} {
		if got, ok := Resolve(telemetryDir, p); !ok || got != want {
			t.Fatalf("resolve(%q) = %q, %t, want %q", p, got, ok, want)
		}
	}
}
