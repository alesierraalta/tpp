# Go testing examples

## Table by behavior partition

```go
func TestProcessInput(t *testing.T) {
	tests := []struct {
		name, input, want string
		wantErr           error
	}{
		{name: "valid", input: "hello", want: "HELLO"},
		{name: "empty", input: "", wantErr: ErrEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProcessInput(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
```

## Fuzz: seeds versus corpus

```go
func FuzzParseIdentifier(f *testing.F) {
	f.Add("valid-name") // runs on every go test
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		got, err := ParseIdentifier(in)
		if err == nil && got == "" {
			t.Fatalf("accepted %q with empty result", in)
		}
	})
}
```

`go test -run='^$' -fuzz=FuzzParseIdentifier -fuzztime=30s`. Crashers are written to `testdata/fuzz/FuzzParseIdentifier/`; commit them so plain `go test` replays them. Prefer round-trip or differential properties over "does not panic".

## HTTP client contract

```go
func TestClientHTTPContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1}`)
	}))
	t.Cleanup(srv.Close)
	resp, err := NewClient(srv.URL).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
```

## Owned state and leak check

```go
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv("APP_MODE", "test")
	dir := t.TempDir()
	t.Chdir(dir)
	if got := LoadConfig(); got.Mode != "test" {
		t.Fatalf("mode = %q", got.Mode)
	}
}
```

## Golden with normalization

```go
var update = flag.Bool("update", false, "update golden files")

func assertGolden(t *testing.T, path, got string) {
	t.Helper()
	got = normalizeOutput(got) // timestamps, absolute paths, locale, CRLF
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Fatalf("golden mismatch %s (-want +got):\n%s", path, diff)
	}
}
```

## Benchmark (Go 1.24)

```go
func BenchmarkParse(b *testing.B) {
	data := loadFixture(b) // setup outside the loop
	for b.Loop() {
		Parse(data)
	}
}
```

## Commands

| Command | Evidence |
|---|---|
| `go test -shuffle=on -count=3 ./pkg` | Order and state independence |
| `go test -race -count=5 ./pkg` | Race detector over repeated runs |
| `go test -short ./...` | Fast scope only; never integration evidence |
| `go test ./pkg -run TestX -update`, then without `-update` | Reviewed golden regeneration and stable replay |
