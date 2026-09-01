package cloud

import (
	"context"
	"testing"
	"time"
)

func TestFinishedImportJobsPruned(t *testing.T) {
	s := NewService(nil)
	s.importMu.Lock()
	s.importJobs["ancient"] = &BatchImportJob{ID: "ancient", StartedAt: time.Now().Add(-2 * time.Hour).UnixMilli()}
	s.importJobs["recent"] = &BatchImportJob{ID: "recent", StartedAt: time.Now().Add(-2 * time.Minute).UnixMilli()}
	s.importMu.Unlock()
	jobID := s.StartBatchImport(context.Background(), BatchImportRequest{})
	if jobID == "" {
		t.Fatal("no job id")
	}
	s.importMu.RLock()
	_, ancientAlive := s.importJobs["ancient"]
	_, recentAlive := s.importJobs["recent"]
	s.importMu.RUnlock()
	if ancientAlive {
		t.Fatal("hour-old finished job not pruned")
	}
	if !recentAlive {
		t.Fatal("recent job wrongly pruned")
	}
}
