// Package assets carries the skill files and host adapters that tpp installs; go:embed only
// reaches files below the package directory, so they live here rather than under internal/.
package assets

import "embed"

// Root holds every file under assets/skills and assets/hosts, dotfiles and underscore names
// included.
//
//go:embed all:skills all:hosts
var Root embed.FS
