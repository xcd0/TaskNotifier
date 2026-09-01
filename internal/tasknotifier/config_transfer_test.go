package tasknotifier

import (
	"path/filepath"
	"testing"
)

func TestExportImportTaskConfigDoesNotCarryRuntimeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := EmptyTaskFile()
	data.Tasks = []Task{{ID: "task", Kind: TaskKindNotification, Enabled: true, Title: "確認", Schedule: Schedule{Type: ScheduleDailyFixed, Time: "10:00", IntervalMinutes: 60}, Notification: NotificationSettings{Method: NotificationDialog}, State: State{LastFiredEvent: "task:2026-09-01"}}}
	data.History = []HistoryEntry{{EventKey: "history"}}

	if err := ExportTaskConfig(path, data); err != nil {
		t.Fatalf("エクスポートできません: %v", err)
	}
	imported, err := ImportTaskConfig(path)
	if err != nil {
		t.Fatalf("インポートできません: %v", err)
	}
	if imported.Tasks[0].State != (State{}) {
		t.Fatalf("実行状態が設定ファイルへ混入しています: %+v", imported.Tasks[0].State)
	}
	if len(imported.History) != 0 {
		t.Fatalf("履歴が設定ファイルへ混入しています: %d", len(imported.History))
	}
}
