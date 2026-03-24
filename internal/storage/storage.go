package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"keeprun/internal/paths"
	"keeprun/internal/task"
)

type Store struct{}

func New() *Store {
	return &Store{}
}

func (s *Store) Save(record task.Record) error {
	if err := paths.EnsureBaseDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task %s: %w", record.Spec.ID, err)
	}
	return atomicWrite(paths.TaskFile(record.Spec.ID), data, 0o644)
}

func (s *Store) Load(id string) (task.Record, error) {
	path := paths.TaskFile(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return task.Record{}, err
	}
	var record task.Record
	if err := json.Unmarshal(data, &record); err != nil {
		return task.Record{}, fmt.Errorf("parse task %s: %w", id, err)
	}
	return record, nil
}

func (s *Store) Delete(id string) error {
	taskPath := paths.TaskFile(id)
	logPath := paths.LogFile(id)
	if err := os.Remove(taskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(logPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) List() ([]task.Record, error) {
	if err := paths.EnsureBaseDirs(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(paths.TasksDir())
	if err != nil {
		return nil, err
	}
	records := make([]task.Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.TasksDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var record task.Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("parse task file %s: %w", entry.Name(), err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Spec.ID < records[j].Spec.ID
	})
	return records, nil
}

func (s *Store) Resolve(ref string) (task.Record, error) {
	records, err := s.List()
	if err != nil {
		return task.Record{}, err
	}
	for _, record := range records {
		if record.Spec.ID == ref || (record.Spec.Name != "" && record.Spec.Name == ref) {
			return record, nil
		}
	}
	return task.Record{}, fmt.Errorf("task %q not found", ref)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
