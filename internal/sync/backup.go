package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/buildinfo"
	"github.com/alesierraalta/tsp/internal/state"
)

type backupEntry struct {
	OriginalPath string `json:"original_path"`
	SnapshotPath string `json:"snapshot_path"`
	Existed      bool   `json:"existed"`
	Mode         uint32 `json:"mode"`
	Kind         string `json:"kind"`
}

type backupManifest struct {
	ID               string        `json:"id"`
	CreatedAt        string        `json:"created_at"`
	RootDir          string        `json:"root_dir"`
	Entries          []backupEntry `json:"entries"`
	Source           string        `json:"source"`
	Description      string        `json:"description"`
	FileCount        int           `json:"file_count"`
	CreatedByVersion string        `json:"created_by_version"`
	Compressed       bool          `json:"compressed"`
	Checksum         string        `json:"checksum"`
}

type backupStore struct {
	root     string
	dir      string
	manifest backupManifest
}

func newBackupStore() (*backupStore, error) {
	statePath, err := state.Path()
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(filepath.Dir(statePath))
	if err != nil {
		return nil, fmt.Errorf("resolve backup root: %w", err)
	}
	return &backupStore{root: root}, nil
}

func (s *backupStore) Snapshot(originalPath string) error {
	info, err := os.Stat(originalPath)
	if err != nil {
		return fmt.Errorf("stat backup source %s: %w", originalPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source %s is not a regular file", originalPath)
	}
	data, err := os.ReadFile(originalPath)
	if err != nil {
		return fmt.Errorf("read backup source %s: %w", originalPath, err)
	}
	if err := s.ensure(); err != nil {
		return err
	}

	absolute, err := filepath.Abs(originalPath)
	if err != nil {
		return fmt.Errorf("resolve backup source %s: %w", originalPath, err)
	}
	snapshotPath := filepath.Join(s.dir, filepath.FromSlash(snapshotRelativePath(absolute)))
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.WriteFile(snapshotPath, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write backup snapshot %s: %w", snapshotPath, err)
	}
	if err := os.Chmod(snapshotPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("preserve backup mode %s: %w", snapshotPath, err)
	}

	s.manifest.Entries = append(s.manifest.Entries, backupEntry{
		OriginalPath: absolute,
		SnapshotPath: snapshotRelativePath(absolute),
		Existed:      true,
		Mode:         uint32(info.Mode().Perm()),
		Kind:         "regular",
	})
	s.manifest.FileCount = len(s.manifest.Entries)
	return s.writeManifest()
}

func (s *backupStore) ensure() error {
	if s.dir != "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(s.root, "backups"), 0o700); err != nil {
		return fmt.Errorf("create backup store: %w", err)
	}
	created := time.Now().UTC()
	baseID := created.Format("20060102150405.000000000")
	for i := 0; ; i++ {
		id := baseID
		if i > 0 {
			id = fmt.Sprintf("%s-%d", baseID, i)
		}
		candidate := filepath.Join(s.root, "backups", id)
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			s.dir = candidate
			s.manifest = backupManifest{
				ID:               id,
				CreatedAt:        created.Format(time.RFC3339Nano),
				RootDir:          s.root,
				Entries:          []backupEntry{},
				Source:           "sync",
				Description:      "snapshot of files overwritten by sync",
				CreatedByVersion: buildinfo.Version,
				Compressed:       false,
			}
			return nil
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create backup snapshot %s: %w", candidate, err)
		}
	}
}

func (s *backupStore) writeManifest() error {
	unsigned := s.manifest
	unsigned.Checksum = ""
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("marshal backup manifest: %w", err)
	}
	digest := sha256.Sum256(payload)
	s.manifest.Checksum = "sha256:" + hex.EncodeToString(digest[:])
	data, err := json.MarshalIndent(s.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(s.dir, "manifest.json"), data, 0o600); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	return nil
}

func (s *backupStore) Dir() string {
	return s.dir
}

func snapshotRelativePath(originalPath string) string {
	clean := filepath.ToSlash(filepath.Clean(originalPath))
	clean = strings.TrimLeft(clean, "/")
	return filepath.ToSlash(filepath.Join("files", clean))
}
