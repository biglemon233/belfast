// 以原子 JSON 文件保存去重和待确认状态，避免依赖独立数据库进程。
//
// Persists deduplication and pending state in atomic JSON files without a database process.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type record struct {
	CreatedAt time.Time    `json:"createdAt"`
	Result    *issueResult `json:"result,omitempty"`
}

type store struct {
	path    string
	records map[string]record
}

func openStore(path string) (*store, error) {
	s := &store{path: path, records: map[string]record{}}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &s.records); err != nil {
		return nil, err
	}
	if s.records == nil || len(s.records) > 10000 {
		return nil, fmt.Errorf("invalid state")
	}
	return s, nil
}

func (s *store) put(id string, entry record) error {
	if _, exists := s.records[id]; !exists && len(s.records) >= 10000 {
		return fmt.Errorf("state capacity reached")
	}
	old, existed := s.records[id]
	s.records[id] = entry
	rollback := func() {
		if existed {
			s.records[id] = old
		} else {
			delete(s.records, id)
		}
	}
	body, err := json.Marshal(s.records)
	if err != nil {
		rollback()
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		rollback()
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.path)
	}
	// Linux 上同步目录项，确保发出 GitHub 请求前待确认记录已持久化。
	if err == nil && runtime.GOOS != "windows" {
		dir, openErr := os.Open(filepath.Dir(s.path))
		err = openErr
		if err == nil {
			err = dir.Sync()
			_ = dir.Close()
		}
	}
	if err != nil {
		rollback()
	}
	return err
}

func (s *store) withinQuota(now time.Time) bool {
	hour, day := 0, 0
	for _, entry := range s.records {
		if now.Sub(entry.CreatedAt) < 24*time.Hour {
			day++
		}
		if now.Sub(entry.CreatedAt) < time.Hour {
			hour++
		}
	}
	return hour < 10 && day < 50
}
