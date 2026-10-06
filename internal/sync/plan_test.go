package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/manifest"
	"github.com/alesierraalta/tsp/internal/state"
)

type planDepsStub struct {
	dirs  map[string][]string
	files map[string][]byte
}

func (s planDepsStub) deps() PlanDeps {
	return PlanDeps{
		ListDir: func(path string) ([]string, error) {
			entries, ok := s.dirs[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return append([]string(nil), entries...), nil
		},
		ReadFile: func(path string) ([]byte, error) {
			data, ok := s.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return append([]byte(nil), data...), nil
		},
	}
}

func planHost() Host {
	return Host{
		Name:      "claude",
		ConfigDir: "/home/test/.claude",
		SkillsDir: "/home/test/.claude/skills",
	}
}

func planSkill() manifest.Component {
	return manifest.Component{ID: "alpha", Kind: manifest.KindSkill, Source: "skills/alpha", Hosts: []string{"*"}}
}

func planFile(rel string, data []byte) manifest.File {
	return manifest.File{
		Component: "alpha",
		Source:    "skills/alpha/" + filepath.ToSlash(rel),
		Rel:       rel,
		SHA256:    planDigest(data),
		Size:      int64(len(data)),
	}
}

func planDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func planInput(host Host, components []manifest.Component, files map[string][]manifest.File, value *state.State) PlanInput {
	return PlanInput{
		Components: components,
		Files:      files,
		State:      value,
		Hosts:      []Host{host},
	}
}

func planState(host Host, assets map[string]state.AssetRecord) *state.State {
	return &state.State{Hosts: map[string]state.HostState{
		host.Name: {ConfigDir: host.ConfigDir, Assets: assets},
	}}
}

func planTree(host Host, component string, files []manifest.File, contents map[string][]byte) map[string][]string {
	dirs := map[string][]string{host.SkillsDir: {component}}
	for _, file := range files {
		current := filepath.Join(host.SkillsDir, component)
		parts := strings.Split(filepath.Clean(filepath.FromSlash(file.Rel)), string(filepath.Separator))
		for _, part := range parts[:len(parts)-1] {
			next := filepath.Join(current, part)
			dirs[current] = appendUnique(dirs[current], part)
			current = next
		}
		if len(parts) > 1 {
			dirs[current] = appendUnique(dirs[current], parts[len(parts)-1])
		}
		contents[filepath.Join(host.SkillsDir, component, filepath.FromSlash(file.Rel))] = contents[file.Rel]
	}
	for path, entries := range dirs {
		sort.Strings(entries)
		dirs[path] = entries
	}
	return dirs
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func filesByRel(files []manifest.File, data map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for _, file := range files {
		result[file.Rel] = append([]byte(nil), data[file.Rel]...)
	}
	return result
}

func actionClasses(actions []Action) []ActionClass {
	classes := make([]ActionClass, 0, len(actions))
	for _, action := range actions {
		classes = append(classes, action.Class)
	}
	return classes
}

func onlyAction(t *testing.T, actions []Action) Action {
	t.Helper()
	if len(actions) != 1 {
		t.Fatalf("actions = %+v, want one action", actions)
	}
	return actions[0]
}

func TestPlanCreatesWhatIsAbsent(t *testing.T) {
	host := planHost()
	files := []manifest.File{planFile("SKILL.md", []byte("new skill")), planFile("bin/run.sh", []byte("new script"))}
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": files}, planState(host, nil))
	cases := []struct {
		name string
		deps PlanDeps
	}{
		{name: "empty disk", deps: (planDepsStub{}).deps()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actions, err := BuildPlan(input, tc.deps)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := actionClasses(actions), []ActionClass{ActionCreate, ActionCreate}; !reflect.DeepEqual(got, want) {
				t.Fatalf("classes = %v, want %v", got, want)
			}
			for _, action := range actions {
				if action.Host != host.Name || action.Component != "alpha" || action.Path == "" {
					t.Errorf("action has wrong ownership: %+v", action)
				}
			}
		})
	}
}

func TestPlanAcceptsAnAlreadyCorrectTree(t *testing.T) {
	host := planHost()
	data := map[string][]byte{"SKILL.md": []byte("new skill"), "bin/run.sh": []byte("new script")}
	files := []manifest.File{planFile("SKILL.md", data["SKILL.md"]), planFile("bin/run.sh", data["bin/run.sh"])}
	contents := filesByRel(files, data)
	dirs := planTree(host, "alpha", files, contents)
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": files}, planState(host, nil))

	actions, err := BuildPlan(input, (planDepsStub{dirs: dirs, files: contents}).deps())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := actionClasses(actions), []ActionClass{ActionOK, ActionOK}; !reflect.DeepEqual(got, want) {
		t.Fatalf("classes = %v, want %v", got, want)
	}
	for _, action := range actions {
		if action.Class != ActionOK {
			t.Fatalf("correct tree contains write action: %+v", action)
		}
	}
}

func TestPlanUpdatesWhatWeWroteAndShippedAhead(t *testing.T) {
	host := planHost()
	old := []byte("old skill")
	new := []byte("new skill")
	file := planFile("SKILL.md", new)
	path := filepath.Join(host.SkillsDir, "alpha", file.Rel)
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": {file}}, planState(host, map[string]state.AssetRecord{path: {SHA256: planDigest(old)}}))

	action := onlyAction(t, mustBuildPlan(t, input, planDepsStub{files: map[string][]byte{path: old}}.deps()))
	if action.Class != ActionUpdate {
		t.Fatalf("class = %q, want %q: %+v", action.Class, ActionUpdate, action)
	}
	if !strings.Contains(action.Reason, "state record matches disk") {
		t.Fatalf("reason = %q, want state comparison", action.Reason)
	}
}

func TestPlanReportsAModifiedFileInsteadOfClobberingIt(t *testing.T) {
	host := planHost()
	manifestData := []byte("new skill")
	diskData := []byte("user edit")
	file := planFile("SKILL.md", manifestData)
	path := filepath.Join(host.SkillsDir, "alpha", file.Rel)
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": {file}}, planState(host, map[string]state.AssetRecord{path: {SHA256: planDigest([]byte("old skill"))}}))

	action := onlyAction(t, mustBuildPlan(t, input, planDepsStub{files: map[string][]byte{path: diskData}}.deps()))
	if action.Class != ActionModified {
		t.Fatalf("class = %q, want %q: %+v", action.Class, ActionModified, action)
	}
	if action.Class == ActionUpdate {
		t.Fatal("modified disk file must never be planned as update")
	}
}

func TestPlanBacksUpAnUntrackedFileAtOneOfOurPaths(t *testing.T) {
	host := planHost()
	manifestData := []byte("new skill")
	diskData := []byte("pre-existing skill")
	file := planFile("SKILL.md", manifestData)
	path := filepath.Join(host.SkillsDir, "alpha", file.Rel)
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": {file}}, planState(host, nil))

	action := onlyAction(t, mustBuildPlan(t, input, planDepsStub{files: map[string][]byte{path: diskData}}.deps()))
	if action.Class != ActionUpdate {
		t.Fatalf("class = %q, want %q", action.Class, ActionUpdate)
	}
	if !strings.Contains(action.Reason, "untracked") || !strings.Contains(action.Reason, "backed up") {
		t.Fatalf("reason = %q, want an untracked backup explanation", action.Reason)
	}
}

func TestPlanNamesForeignFilesAsSkip(t *testing.T) {
	host := planHost()
	foreign := filepath.Join(host.SkillsDir, "alpha", "notes.md")
	deps := planDepsStub{
		dirs: map[string][]string{
			host.SkillsDir:                         {"alpha"},
			filepath.Join(host.SkillsDir, "alpha"): {"notes.md"},
		},
		files: map[string][]byte{foreign: []byte("user notes")},
	}.deps()
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": nil}, planState(host, nil))

	action := onlyAction(t, mustBuildPlan(t, input, deps))
	if action.Class != ActionForeign || action.Component != "" || action.Path != foreign {
		t.Fatalf("foreign action = %+v", action)
	}
	if !strings.Contains(action.Reason, "skipping user file") {
		t.Fatalf("reason = %q, want skip explanation", action.Reason)
	}
}

func TestPlanReportsAnOrphanFromAFormerVersion(t *testing.T) {
	host := planHost()
	orphan := filepath.Join(host.SkillsDir, "old-skill", "SKILL.md")
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": nil}, planState(host, map[string]state.AssetRecord{
		orphan: {SHA256: "old-digest"},
	}))
	input.State.Hosts[host.Name] = state.HostState{
		ConfigDir:  host.ConfigDir,
		Components: []string{"old-skill"},
		Assets:     input.State.Hosts[host.Name].Assets,
	}

	action := onlyAction(t, mustBuildPlan(t, input, (planDepsStub{}).deps()))
	if action.Class != ActionOrphan || action.Path != orphan || action.Component != "old-skill" {
		t.Fatalf("orphan action = %+v", action)
	}
	if !strings.Contains(action.Reason, "manifest no longer ships") {
		t.Fatalf("reason = %q, want former-version explanation", action.Reason)
	}
}

func TestPlanReportsTheHookOncePerHost(t *testing.T) {
	hosts := []Host{
		{Name: "opencode", ConfigDir: "/home/test/.config/opencode", SkillsDir: "/home/test/.config/opencode/skills"},
		planHost(),
	}
	components := []manifest.Component{
		{ID: "hook-a", Kind: manifest.KindHook, Hosts: []string{"*"}},
		{ID: "hook-b", Kind: manifest.KindHook, Hosts: []string{"*"}},
	}
	input := PlanInput{Components: components, State: &state.State{Hosts: map[string]state.HostState{}}, Hosts: hosts}
	actions, err := BuildPlan(input, (planDepsStub{}).deps())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(actions), len(hosts); got != want {
		t.Fatalf("hook actions = %d, want one per host (%d): %+v", got, want, actions)
	}
	for _, action := range actions {
		if action.Class != ActionHook || action.Path != "" || action.Component != "hook-a" {
			t.Errorf("hook action = %+v", action)
		}
		if !strings.Contains(action.Reason, "would wire") {
			t.Errorf("hook reason = %q, want wiring decision", action.Reason)
		}
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	claude := planHost()
	opencode := Host{Name: "opencode", ConfigDir: "/home/test/.config/opencode", SkillsDir: "/home/test/.config/opencode/skills"}
	alphaData := []byte("alpha")
	betaData := []byte("beta")
	componentsA := []manifest.Component{
		{ID: "hook", Kind: manifest.KindHook, Hosts: []string{"*"}},
		{ID: "beta", Kind: manifest.KindSkill, Hosts: []string{"*"}},
		{ID: "alpha", Kind: manifest.KindSkill, Hosts: []string{"*"}},
	}
	componentsB := []manifest.Component{componentsA[2], componentsA[0], componentsA[1]}
	files := map[string][]manifest.File{
		"alpha": {{Component: "alpha", Source: "skills/alpha/SKILL.md", Rel: "SKILL.md", SHA256: planDigest(alphaData)}},
		"beta":  {{Component: "beta", Source: "skills/beta/SKILL.md", Rel: "SKILL.md", SHA256: planDigest(betaData)}},
	}
	inputA := PlanInput{Components: componentsA, Files: files, State: &state.State{Hosts: map[string]state.HostState{}}, Hosts: []Host{opencode, claude}}
	inputB := PlanInput{Components: componentsB, Files: files, State: &state.State{Hosts: map[string]state.HostState{}}, Hosts: []Host{claude, opencode}}

	first, err := BuildPlan(inputA, (planDepsStub{}).deps())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(inputB, (planDepsStub{}).deps())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("plans differ:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	firstReasons := make([]string, len(first))
	secondReasons := make([]string, len(second))
	for i := range first {
		firstReasons[i] = first[i].Reason
		secondReasons[i] = second[i].Reason
	}
	if !reflect.DeepEqual(firstReasons, secondReasons) {
		t.Fatalf("reason sequences differ: %q vs %q", firstReasons, secondReasons)
	}
}

func TestPlanHandlesAMissingDirectoryAndMissingFiles(t *testing.T) {
	host := planHost()
	files := []manifest.File{planFile("SKILL.md", []byte("new skill")), planFile("notes.txt", []byte("new notes"))}
	input := planInput(host, []manifest.Component{planSkill()}, map[string][]manifest.File{"alpha": files}, planState(host, nil))

	actions, err := BuildPlan(input, (planDepsStub{}).deps())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := actionClasses(actions), []ActionClass{ActionCreate, ActionCreate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("classes = %v, want %v", got, want)
	}
}

func mustBuildPlan(t *testing.T, input PlanInput, deps PlanDeps) []Action {
	t.Helper()
	actions, err := BuildPlan(input, deps)
	if err != nil {
		t.Fatal(err)
	}
	return actions
}
