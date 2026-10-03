package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"

	root "github.com/alesierraalta/tsp/assets"
	"github.com/alesierraalta/tsp/internal/assets"
)

func TestEveryEmbeddedSkillIsAComponent(t *testing.T) {
	entries, err := fs.ReadDir(assets.Skills(), ".")
	if err != nil {
		t.Fatalf("read embedded skills: %v", err)
	}
	want := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			want[entry.Name()] = true
		}
	}

	got := map[string]bool{}
	for _, component := range Components() {
		if component.Kind == KindSkill {
			got[component.ID] = true
			if component.Source != "skills/"+component.ID {
				t.Fatalf("component %q source = %q, want %q", component.ID, component.Source, "skills/"+component.ID)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("component skills = %d, want %d: got %v", len(got), len(want), got)
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("embedded skill %q has no component; components = %v", name, got)
		}
	}
	for name := range got {
		if !want[name] {
			t.Fatalf("component %q has no embedded skill; embedded = %v", name, want)
		}
	}
}

func TestEveryFileIsHashedFromTheEmbedFS(t *testing.T) {
	got, err := Files()
	if err != nil {
		t.Fatalf("manifest files: %v", err)
	}
	for _, component := range Components() {
		if component.Kind != KindSkill {
			continue
		}
		files, ok := got[component.ID]
		if !ok {
			t.Fatalf("component %q has no manifest payload", component.ID)
		}
		bySource := map[string]File{}
		for _, file := range files {
			bySource[file.Source] = file
		}
		seen := map[string]bool{}
		err := fs.WalkDir(assets.Skills(), component.ID, func(rel string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(assets.Skills(), rel)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			source := "skills/" + rel
			file, ok := bySource[source]
			if !ok {
				t.Fatalf("embedded file %q has no manifest entry", source)
			}
			seen[source] = true
			wantDigest := hex.EncodeToString(digest[:])
			if file.SHA256 != wantDigest {
				t.Fatalf("file %q digest = %q, want %q", source, file.SHA256, wantDigest)
			}
			if file.Size != int64(len(data)) {
				t.Fatalf("file %q size = %d, want %d", source, file.Size, len(data))
			}
			if file.Rel != strings.TrimPrefix(rel, component.ID+"/") {
				t.Fatalf("file %q relative path = %q, want %q", source, file.Rel, strings.TrimPrefix(rel, component.ID+"/"))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk embedded component %q: %v", component.ID, err)
		}
		if len(seen) != len(files) {
			t.Fatalf("component %q manifest files = %d, embedded files = %d", component.ID, len(files), len(seen))
		}
	}
}

func TestAppliesToHandlesWildcardsAndExplicitHosts(t *testing.T) {
	cases := []struct {
		name string
		host string
		c    Component
		want bool
	}{
		{name: "wildcard host", host: "claude", c: Component{Hosts: []string{"*"}}, want: true},
		{name: "wildcard other host", host: "opencode", c: Component{Hosts: []string{"*"}}, want: true},
		{name: "explicit match", host: "claude", c: Component{Hosts: []string{"claude"}}, want: true},
		{name: "explicit mismatch", host: "opencode", c: Component{Hosts: []string{"claude"}}, want: false},
		{name: "empty wildcard host", host: "", c: Component{Hosts: []string{"*"}}, want: false},
		{name: "empty explicit host", host: "", c: Component{Hosts: []string{"claude"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AppliesTo(tc.host, tc.c); got != tc.want {
				t.Fatalf("AppliesTo(%q, %+v) = %v, want %v", tc.host, tc.c, got, tc.want)
			}
		})
	}
}

func TestComponentFilesRefusesAComponentWithNoPayload(t *testing.T) {
	files, err := ComponentFiles("stop-gate")
	if err == nil {
		t.Fatalf("ComponentFiles(%q) returned files %v without an error", "stop-gate", files)
	}
}

// The Pi extension is an opt-in component whose payload is exactly one embedded file,
// installed under Pi's entry-point name; the adapter's hook settings and its tests are
// shipped files, never manifest payload.
func TestPiExtensionIsOptInAndHashedFromTheEmbeddedFile(t *testing.T) {
	var ext *Component
	for _, component := range Components() {
		if component.ID == "tpp" {
			c := component
			ext = &c
		}
	}
	if ext == nil {
		t.Fatal(`Components() has no "tpp" extension component`)
	}
	if ext.Kind != Kind("extension") || ext.Source != "hosts/pi/tsp.ts" || ext.Default || len(ext.Hosts) != 1 || ext.Hosts[0] != "pi" {
		t.Fatalf("tpp component = %+v, want an opt-in, pi-only extension over hosts/pi/tsp.ts", *ext)
	}
	if !AppliesTo("pi", *ext) || AppliesTo("claude", *ext) {
		t.Fatalf("tpp applies to the wrong hosts: %v", ext.Hosts)
	}

	files, err := Files()
	if err != nil {
		t.Fatalf("manifest files: %v", err)
	}
	payload := files["tpp"]
	if len(payload) != 1 {
		t.Fatalf("tpp payload = %d files, want exactly one: %+v", len(payload), payload)
	}
	file := payload[0]
	if file.Source != "hosts/pi/tsp.ts" || file.Rel != "index.ts" {
		t.Fatalf("tpp file = source %q rel %q, want hosts/pi/tsp.ts installed as index.ts", file.Source, file.Rel)
	}
	want, err := fs.ReadFile(root.Root, "hosts/pi/tsp.ts")
	if err != nil {
		t.Fatalf("hosts/pi/tsp.ts must be embedded: %v", err)
	}
	digest := sha256.Sum256(want)
	if file.SHA256 != hex.EncodeToString(digest[:]) || file.Size != int64(len(want)) {
		t.Fatalf("tpp digest/size = %s/%d, want sha256:%s/%d", file.SHA256, file.Size, hex.EncodeToString(digest[:]), len(want))
	}
	for id, entries := range files {
		for _, entry := range entries {
			if strings.Contains(entry.Source, "settings.stop-hook") || strings.Contains(entry.Source, "tpp.test.mjs") {
				t.Errorf("component %q ships %s; only the extension entry point is installed", id, entry.Source)
			}
		}
	}
}

// Skills must not claim Pi's extension directory, and must keep claiming the four known hosts.
func TestSkillComponentsNameTheirHostsAndSkipPi(t *testing.T) {
	for _, component := range Components() {
		if component.Kind != KindSkill {
			continue
		}
		if AppliesTo("pi", component) {
			t.Errorf("skill %q would install into Pi's extension directory", component.ID)
		}
		for _, host := range []string{"claude", "opencode", "gemini", "codex"} {
			if !AppliesTo(host, component) {
				t.Errorf("skill %q does not apply to %s", component.ID, host)
			}
		}
	}
}
