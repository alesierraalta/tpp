package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillsDigestIsStableAndSensitiveToFileBytes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"alpha/SKILL.md": "alpha\n",
		"beta.md":        "beta\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := SkillsDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SkillsDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest changed across identical calls: %q != %q", first, second)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", "SKILL.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := SkillsDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatalf("digest did not change when file bytes changed: %q", changed)
	}
}

func TestSkillsDigestAcceptsConfigDirectory(t *testing.T) {
	config := t.TempDir()
	if err := writeEmbeddedSkills(config); err != nil {
		t.Fatal(err)
	}
	direct, err := SkillsDigest(filepath.Join(config, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	fromConfig, err := SkillsDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	if direct != fromConfig {
		t.Fatalf("config and skills roots must digest identically: %q != %q", direct, fromConfig)
	}
}

func TestVerifyBenchSkillsAcceptsEmbeddedTreeAndNamesDifferences(t *testing.T) {
	t.Run("accepts embedded tree", func(t *testing.T) {
		config := t.TempDir()
		if err := writeEmbeddedSkills(config); err != nil {
			t.Fatal(err)
		}
		if err := VerifyBenchSkills(config); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rejects extra file", func(t *testing.T) {
		config := t.TempDir()
		if err := writeEmbeddedSkills(config); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(config, "skills", "operator-leak.md")
		if err := os.WriteFile(path, []byte("answer"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerifyBenchSkills(config); err == nil || !strings.Contains(err.Error(), "operator-leak.md") {
			t.Fatalf("extra file error = %v, want its relative path", err)
		}
	})

	t.Run("rejects modified file", func(t *testing.T) {
		config := t.TempDir()
		if err := writeEmbeddedSkills(config); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(config, "skills", "test-strategy", "SKILL.md")
		if err := os.WriteFile(path, []byte("operator answer"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerifyBenchSkills(config); err == nil || !strings.Contains(err.Error(), "test-strategy/SKILL.md") {
			t.Fatalf("modified file error = %v, want its relative path", err)
		}
	})
}

func TestRunRecordsProvenance(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	var globs []string
	for _, name := range []string{"case-a", "case-b"} {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	agg, code := Run(Options{
		CasesGlob: strings.Join(globs, ","), Model: "model-under-test", Runner: RunnerPi,
		Runs: 2, ConfigMode: ConfigBench, Timeout: time.Minute, SuiteTimeout: time.Minute,
		Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent,
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	p := agg.Provenance
	if p.MetricsVersion != MetricsVersion || p.Model != "model-under-test" || p.Runner != RunnerPi || p.Runs != 2 || p.AgentConfig != ConfigBench {
		t.Fatalf("provenance options = %+v", p)
	}
	if p.Cases != 2 {
		t.Fatalf("provenance cases = %d, want the two distinct cases", p.Cases)
	}
	if p.Corpus == "" || p.Corpus != agg.Corpus {
		t.Fatalf("provenance corpus = %q, aggregate corpus = %q", p.Corpus, agg.Corpus)
	}
}
