// Package assets exposes the embedded skills as a filesystem rooted at the skills directory.
package assets

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	root "github.com/alesierraalta/tpp/assets"
)

// Skills returns the embedded skill tree: one directory per skill, each with a SKILL.md.
func Skills() fs.FS {
	sub, err := fs.Sub(root.Root, "skills")
	if err != nil {
		panic("embedded skills missing: " + err.Error())
	}
	return sub
}

// SkillNames lists the embedded skills in a stable order.
func SkillNames() []string {
	entries, err := fs.ReadDir(Skills(), ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Hosts returns the embedded host adapter tree: one directory per host under assets/hosts.
func Hosts() fs.FS {
	sub, err := fs.Sub(root.Root, "hosts")
	if err != nil {
		panic("embedded host adapters missing: " + err.Error())
	}
	return sub
}

// Tree resolves an embedded source path to the filesystem that carries it and the path inside
// that filesystem: "skills/<skill>/..." lives in Skills, "hosts/<host>/..." in Hosts. The
// manifest hashes through this seam and the writer reads through it, so one digest spans both
// embed roots without a second installer.
func Tree(source string) (fs.FS, string, error) {
	if rel, ok := strings.CutPrefix(source, "skills/"); ok {
		return Skills(), rel, nil
	}
	if rel, ok := strings.CutPrefix(source, "hosts/"); ok {
		return Hosts(), rel, nil
	}
	return nil, "", fmt.Errorf("embedded source %q is outside the skills and hosts roots", source)
}
