package tasknotifier

import (
	"path/filepath"
	"testing"
	"time"
)

func limitedTestData(t *testing.T, once bool, count int) TaskFile {
	t.Helper()
	task, err := NewTask()
	if err != nil {
		t.Fatal(err)
	}
	task.Title = "回数制限"
	task.Schedule.MaxOccurrences = count
	if once {
		task.Schedule.Type = ScheduleOnce
		task.Schedule.Date = "2026-10-01"
	}
	data := EmptyTaskFile()
	data.Tasks = []Task{task}
	return data
}

func TestOnceFutureOverdueSnoozeAndRestart(t *testing.T) {
	data := limitedTestData(t, true, 0)
	before := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	event, ok := NextEvent(data, before)
	if !ok || event.ScheduledAt.Format("2006-01-02 15:04") != "2026-10-01 09:00" {
		t.Fatalf("future event: %+v, %v", event, ok)
	}
	if len(DueEvents(data, before)) != 0 {
		t.Fatal("fired before the specified date")
	}
	now := before.AddDate(0, 2, 0)
	due := DueEvents(data, now)
	if len(due) != 1 {
		t.Fatalf("overdue events: %v", due)
	}
	data, err := Snooze(data, due[0], now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(DueEvents(data, now)) != 0 || data.Tasks[0].State.FiredCount != 0 {
		t.Fatal("snooze consumed an occurrence or fired immediately")
	}
	due = DueEvents(data, now.Add(10*time.Minute))
	if len(due) != 1 || due[0].Key != event.Key {
		t.Fatal("snooze lost the single occurrence")
	}
	data, err = Acknowledge(data, due[0])
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(t.TempDir(), TaskFileName))
	if _, err := store.Save(data); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tasks[0].State.Completed || loaded.Tasks[0].State.FiredCount != 1 {
		t.Fatalf("state not persisted: %+v", loaded.Tasks[0].State)
	}
	if _, ok := NextEvent(loaded, now); ok {
		t.Fatal("completed single task scheduled again")
	}
}

func TestLimitedCountAcrossDaysAndDuplicateAcknowledgement(t *testing.T) {
	data := limitedTestData(t, false, 3)
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	for day := 0; day < 3; day++ {
		due := DueEvents(data, now.AddDate(0, 0, day))
		if len(due) != 1 {
			t.Fatalf("day %d: %v", day, due)
		}
		var err error
		data, err = Acknowledge(data, due[0])
		if err != nil {
			t.Fatal(err)
		}
		data, err = Acknowledge(data, due[0])
		if err != nil || data.Tasks[0].State.FiredCount != day+1 {
			t.Fatalf("duplicate acknowledgement: %+v, %v", data.Tasks[0].State, err)
		}
	}
	if len(DueEvents(data, now.AddDate(0, 0, 3))) != 0 {
		t.Fatal("exceeded the count limit")
	}
	if _, ok := NextEvent(data, now.AddDate(0, 0, 3)); ok {
		t.Fatal("completed task has a next event")
	}
	before := data.Tasks[0]
	edited := before
	edited.Title = "タイトルのみ変更"
	if ApplyEdit(before, edited).State.FiredCount != 3 {
		t.Fatal("title edit reset the count")
	}
	edited.Schedule.MaxOccurrences = 4
	if ApplyEdit(before, edited).State.FiredCount != 0 {
		t.Fatal("schedule edit did not reset the count")
	}
}

func TestLimitedIntervalSkipsMissedSlotsWithoutConsumingCount(t *testing.T) {
	data := limitedTestData(t, false, 2)
	data.Tasks[0].Schedule.RepeatEnabled = true
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for n := 0; n < 2; n++ {
		due := DueEvents(data, now.Add(time.Duration(n)*time.Hour))
		if len(due) != 1 {
			t.Fatalf("expected one due notification: %v", due)
		}
		var err error
		data, err = Acknowledge(data, due[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(DueEvents(data, now.Add(2*time.Hour))) != 0 || data.Tasks[0].State.FiredCount != 2 {
		t.Fatal("interval count limit failed")
	}
}

func TestUnlimitedTasksRemainRecurring(t *testing.T) {
	data := limitedTestData(t, false, 0)
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	for day := 0; day < 5; day++ {
		due := DueEvents(data, now.AddDate(0, 0, day))
		if len(due) != 1 {
			t.Fatal("unlimited task stopped")
		}
		var err error
		data, err = Acknowledge(data, due[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	if data.Tasks[0].State.Completed {
		t.Fatal("unlimited task was completed")
	}
}

func TestLimitedTaskValidation(t *testing.T) {
	valid := limitedTestData(t, true, 0).Tasks[0]
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*Task){
		func(task *Task) { task.Schedule.Date = "2026-02-30" },
		func(task *Task) { task.Schedule.RepeatEnabled = true },
		func(task *Task) { task.Schedule.MaxOccurrences = -1 },
		func(task *Task) { task.Schedule.MaxOccurrences = 2 },
		func(task *Task) { task.Condition.WeekdaysEnabled = true },
	} {
		task := valid
		modify(&task)
		if task.Validate() == nil {
			t.Fatalf("accepted invalid task: %+v", task)
		}
	}
}
