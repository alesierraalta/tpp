package feedback

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alesierraalta/tsp/internal/sanitize"
)

const reviewCursorSchemaVersion = 1

// ReviewBatch is the set of reports pending operator review.
type ReviewBatch struct {
	Reports       []Report
	Token         string
	Initialized   bool
	BaselineCount int
	SnapshotCount int
}

// ReviewMark describes the ledger prefix acknowledged by a review.
type ReviewMark struct {
	Through       int
	NewlyReviewed int
}

type reviewCursor struct {
	SchemaVersion  int    `json:"schema_version"`
	BaselineCount  int    `json:"baseline_count"`
	BaselineDigest string `json:"baseline_digest"`
	CurrentCount   int    `json:"current_count"`
	CurrentDigest  string `json:"current_digest"`
}

// Pending returns reports appended after the review cursor.
func Pending(configDir string) (ReviewBatch, error) {
	reports, canonicalRows, err := readReviewLedger(configDir)
	if err != nil {
		return ReviewBatch{}, err
	}

	cursor, exists, err := readReviewCursor(configDir)
	if err != nil {
		return ReviewBatch{}, err
	}
	if !exists {
		digest := reviewPrefixDigest(canonicalRows, len(canonicalRows))
		cursor = reviewCursor{
			SchemaVersion:  reviewCursorSchemaVersion,
			BaselineCount:  len(canonicalRows),
			BaselineDigest: digest,
			CurrentCount:   len(canonicalRows),
			CurrentDigest:  digest,
		}
		if err := writeReviewCursor(configDir, cursor); err != nil {
			return ReviewBatch{}, err
		}
		return ReviewBatch{
			Reports:       []Report{},
			Initialized:   true,
			BaselineCount: cursor.BaselineCount,
			SnapshotCount: len(reports),
		}, nil
	}
	if err := validateReviewCursor(cursor, canonicalRows); err != nil {
		return ReviewBatch{}, err
	}

	pending := make([]Report, len(reports)-cursor.CurrentCount)
	for i, report := range reports[cursor.CurrentCount:] {
		pending[i] = resolveReviewReport(configDir, report)
	}
	digest := reviewPrefixDigest(canonicalRows, len(canonicalRows))
	return ReviewBatch{
		Reports:       pending,
		Token:         formatReviewToken(len(canonicalRows), digest),
		BaselineCount: cursor.BaselineCount,
		SnapshotCount: len(reports),
	}, nil
}

// MarkReviewed acknowledges the exact ledger prefix identified by token.
func MarkReviewed(configDir, token string) (ReviewMark, error) {
	reports, canonicalRows, err := readReviewLedger(configDir)
	if err != nil {
		return ReviewMark{}, err
	}
	cursor, exists, err := readReviewCursor(configDir)
	if err != nil {
		return ReviewMark{}, err
	}
	if !exists {
		return ReviewMark{}, fmt.Errorf("review cursor is not initialized")
	}
	if err := validateReviewCursor(cursor, canonicalRows); err != nil {
		return ReviewMark{}, err
	}

	count, digest, err := parseReviewToken(token)
	if err != nil {
		return ReviewMark{}, err
	}
	if count < cursor.CurrentCount {
		return ReviewMark{}, fmt.Errorf("review token moves backward from %d to %d", cursor.CurrentCount, count)
	}
	if count > len(reports) {
		return ReviewMark{}, fmt.Errorf("review token count %d exceeds ledger count %d", count, len(reports))
	}
	actualDigest := reviewPrefixDigest(canonicalRows, count)
	if digest != actualDigest {
		return ReviewMark{}, fmt.Errorf("review token does not match ledger prefix through %d", count)
	}
	if count == cursor.CurrentCount {
		if digest != cursor.CurrentDigest {
			return ReviewMark{}, fmt.Errorf("review token does not match current cursor")
		}
		return ReviewMark{Through: count}, nil
	}

	newlyReviewed := count - cursor.CurrentCount
	cursor.CurrentCount = count
	cursor.CurrentDigest = digest
	if err := writeReviewCursor(configDir, cursor); err != nil {
		return ReviewMark{}, err
	}
	return ReviewMark{Through: count, NewlyReviewed: newlyReviewed}, nil
}

func reviewCursorSidecarPath(configDir string) string {
	return filepath.Join(sanitize.TelemetryDir(configDir), "run-feedback.reviewed.json")
}

func readReviewCursor(configDir string) (reviewCursor, bool, error) {
	path := reviewCursorSidecarPath(configDir)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return reviewCursor{}, false, nil
	}
	if err != nil {
		return reviewCursor{}, false, fmt.Errorf("stat review cursor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return reviewCursor{}, false, fmt.Errorf("review cursor is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return reviewCursor{}, false, fmt.Errorf("read review cursor: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor reviewCursor
	if err := decoder.Decode(&cursor); err != nil {
		return reviewCursor{}, false, fmt.Errorf("decode review cursor: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return reviewCursor{}, false, fmt.Errorf("decode review cursor: multiple JSON values")
		}
		return reviewCursor{}, false, fmt.Errorf("decode review cursor: %w", err)
	}
	if err := validateReviewCursorShape(cursor); err != nil {
		return reviewCursor{}, false, err
	}
	return cursor, true, nil
}

func validateReviewCursorShape(cursor reviewCursor) error {
	if cursor.SchemaVersion != reviewCursorSchemaVersion {
		return fmt.Errorf("unsupported review cursor schema version %d", cursor.SchemaVersion)
	}
	if cursor.BaselineCount < 0 || cursor.CurrentCount < cursor.BaselineCount {
		return fmt.Errorf("invalid review cursor counts: baseline %d, current %d", cursor.BaselineCount, cursor.CurrentCount)
	}
	if !validReviewDigest(cursor.BaselineDigest) || !validReviewDigest(cursor.CurrentDigest) {
		return fmt.Errorf("invalid review cursor digest")
	}
	if cursor.CurrentCount == cursor.BaselineCount && cursor.CurrentDigest != cursor.BaselineDigest {
		return fmt.Errorf("review cursor baseline and current digests disagree")
	}
	return nil
}

func validateReviewCursor(cursor reviewCursor, canonicalRows [][]byte) error {
	if err := validateReviewCursorShape(cursor); err != nil {
		return err
	}
	if cursor.BaselineCount > len(canonicalRows) {
		return fmt.Errorf("review baseline count %d exceeds ledger count %d", cursor.BaselineCount, len(canonicalRows))
	}
	if cursor.CurrentCount > len(canonicalRows) {
		return fmt.Errorf("reviewed count %d exceeds ledger count %d", cursor.CurrentCount, len(canonicalRows))
	}
	if digest := reviewPrefixDigest(canonicalRows, cursor.BaselineCount); digest != cursor.BaselineDigest {
		return fmt.Errorf("review baseline prefix drift detected")
	}
	if digest := reviewPrefixDigest(canonicalRows, cursor.CurrentCount); digest != cursor.CurrentDigest {
		return fmt.Errorf("reviewed prefix drift detected")
	}
	return nil
}

func readReviewLedger(configDir string) ([]Report, [][]byte, error) {
	data, err := os.ReadFile(LedgerPath(configDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read feedback ledger: %w", err)
	}

	var reports []Report
	var canonicalRows [][]byte
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var report Report
		if err := json.Unmarshal([]byte(line), &report); err != nil {
			return nil, nil, fmt.Errorf("ledger line %d: %w", i+1, err)
		}
		canonical, err := json.Marshal(report)
		if err != nil {
			return nil, nil, fmt.Errorf("canonicalize ledger line %d: %w", i+1, err)
		}
		reports = append(reports, report)
		canonicalRows = append(canonicalRows, canonical)
	}
	return reports, canonicalRows, nil
}

func reviewPrefixDigest(canonicalRows [][]byte, count int) string {
	hash := sha256.New()
	for _, row := range canonicalRows[:count] {
		_, _ = hash.Write(row)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func resolveReviewReport(configDir string, report Report) Report {
	if report.Sanitized {
		telemetryDir := sanitize.TelemetryDir(configDir)
		if original, ok := sanitize.Resolve(telemetryDir, report.Repo); ok {
			report.Repo = original
		}
		if original, ok := sanitize.Resolve(telemetryDir, report.Plan); ok {
			report.Plan = original
		}
	}
	return report
}

func validReviewDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && hex.EncodeToString(decoded) == digest
}

func formatReviewToken(count int, digest string) string {
	return "v1:" + strconv.Itoa(count) + ":" + digest
}

func parseReviewToken(token string) (int, string, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 || parts[0] != "v1" {
		return 0, "", fmt.Errorf("malformed review token")
	}
	count, err := strconv.Atoi(parts[1])
	if err != nil || count < 0 || strconv.Itoa(count) != parts[1] || !validReviewDigest(parts[2]) {
		return 0, "", fmt.Errorf("malformed review token")
	}
	return count, parts[2], nil
}

func writeReviewCursor(configDir string, cursor reviewCursor) error {
	path := reviewCursorSidecarPath(configDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create review cursor directory: %w", err)
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return fmt.Errorf("encode review cursor: %w", err)
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(dir, ".run-feedback.reviewed-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary review cursor: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect temporary review cursor: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary review cursor: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary review cursor: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary review cursor: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace review cursor: %w", err)
	}
	return nil
}
