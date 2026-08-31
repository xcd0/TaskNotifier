package tasknotifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreMigratesRuntimeStateOutOfTasksJSON(t *testing.T) {
	directory := t.TempDir()
	tasksPath := filepath.Join(directory, TaskFileName)

	task, err := NewTask()
	if err != nil {
		t.Fatal(err)
	}
	task.Title = "test"
	task.State.SnoozeUntil = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	data := EmptyTaskFile()
	data.Tasks = append(data.Tasks, task)
	data.History = append(data.History, HistoryEntry{
		EventKey:    "event",
		TaskID:      task.ID,
		TaskTitle:   task.Title,
		ScheduledAt: time.Now().UTC().Format(time.RFC3339),
		NotifiedAt:  time.Now().UTC().Format(time.RFC3339),
		Method:      NotificationDialog,
		Result:      "表示済み",
	})

	encoded, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasksPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(tasksPath)
	loaded, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tasks[0].State.SnoozeUntil != task.State.SnoozeUntil {
		t.Fatalf("runtime state was not preserved: %q", loaded.Tasks[0].State.SnoozeUntil)
	}
	if len(loaded.History) != 1 {
		t.Fatalf("history was not preserved: %d", len(loaded.History))
	}
	if _, err := os.Stat(store.StatePath()); err != nil {
		t.Fatalf("state.json was not created: %v", err)
	}

	config, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	if strings.Contains(text, `"history"`) || strings.Contains(text, `"state"`) {
		t.Fatalf("runtime data remains in tasks.json: %s", text)
	}
	if _, err := os.Stat(tasksPath + ".bak"); err != nil {
		t.Fatalf("tasks.json.bak was not created during migration: %v", err)
	}
}

func TestStoreSaveKeepsConfigAndStateSeparate(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(filepath.Join(directory, TaskFileName))
	data, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	task, err := NewTask()
	if err != nil {
		t.Fatal(err)
	}
	task.Title = "save-test"
	task.State.LastFiredEvent = "event-key"
	data.Tasks = append(data.Tasks, task)
	if _, err := store.Save(data); err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), `"state"`) {
		t.Fatalf("tasks.json contains task state: %s", string(config))
	}
	state, err := os.ReadFile(store.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "event-key") {
		t.Fatalf("state.json does not contain runtime state: %s", string(state))
	}
	if _, err := os.Stat(store.Path() + ".bak"); err != nil {
		t.Fatalf("tasks.json.bak was not created: %v", err)
	}
}
