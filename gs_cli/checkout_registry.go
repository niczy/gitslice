package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CheckoutRecord struct {
	Path       string `json:"path"`
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

type checkoutRegistry struct {
	Entries []CheckoutRecord `json:"entries"`
}

func checkoutRegistryPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "checkouts.json"), nil
}

func readCheckoutRegistry() (checkoutRegistry, error) {
	path, err := checkoutRegistryPath()
	if err != nil {
		return checkoutRegistry{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkoutRegistry{}, nil
		}
		return checkoutRegistry{}, err
	}
	var registry checkoutRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return checkoutRegistry{}, err
	}
	return registry, nil
}

func writeCheckoutRegistry(registry checkoutRegistry) error {
	path, err := checkoutRegistryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func listCheckoutRecords() ([]CheckoutRecord, error) {
	registry, err := readCheckoutRegistry()
	if err != nil {
		return nil, err
	}
	sortCheckoutRecords(registry.Entries)
	return registry.Entries, nil
}

func registerCheckout(path, sliceID, commitHash string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absPath = filepath.Clean(absPath)

	registry, err := readCheckoutRegistry()
	if err != nil {
		return err
	}

	record := CheckoutRecord{
		Path:       absPath,
		SliceID:    strings.TrimSpace(sliceID),
		CommitHash: strings.TrimSpace(commitHash),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	replaced := false
	for i := range registry.Entries {
		if filepath.Clean(registry.Entries[i].Path) == absPath {
			registry.Entries[i] = record
			replaced = true
			break
		}
	}
	if !replaced {
		registry.Entries = append(registry.Entries, record)
	}
	sortCheckoutRecords(registry.Entries)
	return writeCheckoutRegistry(registry)
}

func removeCheckoutRecord(path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	absPath = filepath.Clean(absPath)

	registry, err := readCheckoutRegistry()
	if err != nil {
		return false, err
	}

	filtered := make([]CheckoutRecord, 0, len(registry.Entries))
	removed := false
	for _, entry := range registry.Entries {
		if filepath.Clean(entry.Path) == absPath {
			removed = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !removed {
		return false, nil
	}
	registry.Entries = filtered
	sortCheckoutRecords(registry.Entries)
	return true, writeCheckoutRegistry(registry)
}

func clearCheckoutRegistry() error {
	path, err := checkoutRegistryPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func pruneStaleCheckoutRecords() (int, error) {
	registry, err := readCheckoutRegistry()
	if err != nil {
		return 0, err
	}

	filtered := make([]CheckoutRecord, 0, len(registry.Entries))
	removed := 0
	for _, entry := range registry.Entries {
		if checkoutRecordIsStale(entry) {
			removed++
			continue
		}
		filtered = append(filtered, entry)
	}
	if removed == 0 {
		return 0, nil
	}
	registry.Entries = filtered
	sortCheckoutRecords(registry.Entries)
	return removed, writeCheckoutRegistry(registry)
}

func checkoutRecordIsStale(record CheckoutRecord) bool {
	cleanPath := filepath.Clean(strings.TrimSpace(record.Path))
	if cleanPath == "" || cleanPath == "." {
		return true
	}
	info, err := os.Stat(cleanPath)
	if err != nil || !info.IsDir() {
		return true
	}
	sliceID, err := readSliceIDFromCheckoutPath(cleanPath)
	if err != nil {
		return true
	}
	return strings.TrimSpace(sliceID) != strings.TrimSpace(record.SliceID)
}

func readSliceIDFromCheckoutPath(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, sliceConfigPath))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func countUniqueCheckoutSlices(records []CheckoutRecord) int {
	if len(records) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		sliceID := strings.TrimSpace(record.SliceID)
		if sliceID == "" {
			continue
		}
		seen[sliceID] = struct{}{}
	}
	return len(seen)
}

func countStaleCheckoutRecords(records []CheckoutRecord) int {
	stale := 0
	for _, record := range records {
		if checkoutRecordIsStale(record) {
			stale++
		}
	}
	return stale
}

func sortCheckoutRecords(records []CheckoutRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].SliceID == records[j].SliceID {
			return records[i].Path < records[j].Path
		}
		return records[i].SliceID < records[j].SliceID
	})
}
