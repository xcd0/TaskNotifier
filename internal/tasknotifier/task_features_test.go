package tasknotifier

import (
	"testing"
	"time"
)

func TestDuplicateTaskResetsRuntimeState(t *testing.T) {
	data := EmptyTaskFile()
	data.Tasks = []Task{{ID: "original", Kind: TaskKindNotification, Enabled: true, Title: "確認", Schedule: Schedule{Type: ScheduleDailyFixed, Time: "10:00", IntervalMinutes: 60}, Notification: NotificationSettings{Method: NotificationDialog}, State: State{LastFiredEvent: "original:2026-09-01", PausedUntil: "2026-09-02T00:00:00+09:00"}}}

	duplicated, id, err := DuplicateTask(data, "original")
	if err != nil {
		t.Fatalf("複製できません: %v", err)
	}
	if id == "" || id == "original" {
		t.Fatalf("新しいIDが不正です: %q", id)
	}
	if len(duplicated.Tasks) != 2 {
		t.Fatalf("タスク数が不正です: %d", len(duplicated.Tasks))
	}
	if duplicated.Tasks[1].State != (State{}) {
		t.Fatalf("複製タスクの状態が初期化されていません: %+v", duplicated.Tasks[1].State)
	}
}

func TestPauseTaskWorkEnd(t *testing.T) {
	location := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 9, 1, 10, 30, 0, 0, location)
	data := EmptyTaskFile()
	data.Tasks = []Task{{ID: "task", Kind: TaskKindNotification, Enabled: true, Title: "確認", Schedule: Schedule{Type: ScheduleDailyFixed, Time: "11:00", IntervalMinutes: 60}, Notification: NotificationSettings{Method: NotificationDialog}}}

	paused, err := PauseTask(data, "task", PauseModeWorkEnd, now)
	if err != nil {
		t.Fatalf("一時停止できません: %v", err)
	}
	until, err := time.Parse(time.RFC3339, paused.Tasks[0].State.PausedUntil)
	if err != nil {
		t.Fatalf("一時停止日時が不正です: %v", err)
	}
	if until.Hour() != 18 || until.Minute() != 0 {
		t.Fatalf("勤務終了日時が不正です: %v", until)
	}
}

func TestPausedTaskDoesNotProduceEvent(t *testing.T) {
	location := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 9, 1, 10, 30, 0, 0, location)
	data := EmptyTaskFile()
	data.Tasks = []Task{{
		ID:           "paused",
		Kind:         TaskKindNotification,
		Enabled:      true,
		Title:        "確認",
		Schedule:     Schedule{Type: ScheduleDailyFixed, Time: "10:00", IntervalMinutes: 60},
		Notification: NotificationSettings{Method: NotificationDialog},
		State:        State{PausedUntil: now.Add(time.Hour).Format(time.RFC3339)},
	}}
	if events := DueEvents(data, now); len(events) != 0 {
		t.Fatalf("一時停止中タスクが通知対象になっています: %#v", events)
	}
}
