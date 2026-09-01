package tasknotifier

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

const targetDateLayout = "2006-01-02"

type Event struct {
	TaskID      string
	TaskTitle   string
	Key         string
	TargetDate  string
	ScheduledAt time.Time
	DueAt       time.Time
	IsTest      bool
}
type Occurrence struct {
	TargetDate  string
	EventKey    string
	ScheduledAt time.Time
}

func OccurrenceForDate(task Task, target time.Time) (Occurrence, error) {
	o, e := OccurrencesForDate(task, target, nil)
	if e != nil {
		return Occurrence{}, e
	}
	if len(o) == 0 {
		return Occurrence{}, fmt.Errorf("対象日の通知候補がありません")
	}
	return o[0], nil
}
func OccurrencesForDate(task Task, target time.Time, periods []Period) ([]Occurrence, error) {
	location := target.Location()
	targetDate := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, location)
	when, err := firstScheduledTime(task, targetDate)
	if err != nil {
		return nil, err
	}
	dateText := targetDate.Format(targetDateLayout)
	if !task.Schedule.RepeatEnabled {
		o := Occurrence{TargetDate: dateText, EventKey: task.ID + ":" + dateText, ScheduledAt: when}
		if occurrenceMatchesCondition(task, periods, o.ScheduledAt) {
			return []Occurrence{o}, nil
		}
		return []Occurrence{}, nil
	}
	interval := time.Duration(task.Schedule.IntervalMinutes) * time.Minute
	if interval <= 0 {
		return nil, fmt.Errorf("通知間隔は1分以上である必要があります")
	}
	end := time.Date(when.Year(), when.Month(), when.Day(), 23, 59, 59, 0, location)
	if task.Schedule.EndEnabled {
		h, m, e := ParseClock(task.Schedule.EndTime)
		if e != nil {
			return nil, e
		}
		end = time.Date(when.Year(), when.Month(), when.Day(), h, m, 0, 0, location)
		if end.Before(when) {
			end = end.AddDate(0, 0, 1)
		}
	}
	var r []Occurrence
	for index, scheduled := 0, when; !scheduled.After(end) && index < 1441; index, scheduled = index+1, scheduled.Add(interval) {
		if !occurrenceMatchesCondition(task, periods, scheduled) {
			continue
		}
		r = append(r, Occurrence{TargetDate: dateText, EventKey: fmt.Sprintf("%s:%s:%04d", task.ID, dateText, index), ScheduledAt: scheduled})
	}
	return r, nil
}
func firstScheduledTime(task Task, targetDate time.Time) (time.Time, error) {
	h, m, e := ParseClock(task.Schedule.Time)
	if e != nil {
		return time.Time{}, e
	}
	when := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, targetDate.Location())
	switch task.Schedule.Type {
	case ScheduleDailyFixed:
	case ScheduleDailyRandomAfter:
		when = when.Add(time.Duration(DeterministicOffset(task.ID, targetDate, task.Schedule.Minutes)) * time.Minute)
	case ScheduleDailyBefore:
		when = when.Add(-time.Duration(task.Schedule.Minutes) * time.Minute)
	default:
		return time.Time{}, fmt.Errorf("未対応のschedule.typeです: %q", task.Schedule.Type)
	}
	return when, nil
}
func occurrenceMatchesCondition(task Task, periods []Period, scheduled time.Time) bool {
	if task.Condition.PeriodEnabled {
		p, ok := PeriodByID(periods, task.Condition.PeriodID)
		if !ok || !p.Contains(scheduled) {
			return false
		}
	}
	if task.Condition.WeekdaysEnabled {
		wanted := WeekdayKey(scheduled.Weekday())
		for _, w := range task.Condition.Weekdays {
			if w == wanted {
				return true
			}
		}
		return false
	}
	return true
}
func DeterministicOffset(taskID string, targetDate time.Time, maximum int) int {
	if maximum <= 0 {
		return 0
	}
	h := sha256.Sum256([]byte(taskID + "\n" + targetDate.Format(targetDateLayout)))
	v := binary.BigEndian.Uint64(h[:8])
	return int(v % uint64(maximum+1))
}
func DueEvents(data TaskFile, now time.Time) []Event {
	var events []Event
	for i := range data.Tasks {
		task := data.Tasks[i]
		if task.Kind == TaskKindTodo || !task.Enabled || !NotificationEnabled(task) || taskPaused(task, now) {
			continue
		}
		if task.State.SnoozeUntil != "" {
			if e, ok := dueSnoozedEvent(task, data.Periods, now); ok {
				events = append(events, e)
			}
			continue
		}
		var latest Occurrence
		found := false
		for _, o := range relevantOccurrences(task, data.Periods, now) {
			if o.ScheduledAt.After(now) || eventAcknowledged(task, o) {
				continue
			}
			if !found || o.ScheduledAt.After(latest.ScheduledAt) {
				latest = o
				found = true
			}
		}
		if found {
			events = append(events, eventFromOccurrence(task, latest, latest.ScheduledAt))
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].DueAt.Equal(events[j].DueAt) {
			return events[i].TaskTitle < events[j].TaskTitle
		}
		return events[i].DueAt.Before(events[j].DueAt)
	})
	return events
}
func NextEvent(data TaskFile, now time.Time) (Event, bool) {
	if due := DueEvents(data, now); len(due) > 0 {
		due[0].DueAt = now
		return due[0], true
	}
	var c []Event
	for i := range data.Tasks {
		task := data.Tasks[i]
		if task.Kind == TaskKindTodo || !task.Enabled || !NotificationEnabled(task) || taskPaused(task, now) {
			continue
		}
		if task.State.SnoozeUntil != "" {
			if s, e := time.Parse(time.RFC3339, task.State.SnoozeUntil); e == nil && s.After(now) {
				if o, ok := occurrenceForSnooze(task, data.Periods, s); ok {
					c = append(c, eventFromOccurrence(task, o, s))
				}
				continue
			}
		}
		for day := -2; day <= 3; day++ {
			target := localDay(now).AddDate(0, 0, day)
			os, e := OccurrencesForDate(task, target, data.Periods)
			if e != nil {
				continue
			}
			for _, o := range os {
				if !o.ScheduledAt.After(now) || eventAcknowledged(task, o) {
					continue
				}
				c = append(c, eventFromOccurrence(task, o, o.ScheduledAt))
			}
		}
	}
	if len(c) == 0 {
		return Event{}, false
	}
	sort.SliceStable(c, func(i, j int) bool { return c[i].DueAt.Before(c[j].DueAt) })
	return c[0], true
}
func taskPaused(task Task, now time.Time) bool {
	if task.State.PausedUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, task.State.PausedUntil)
	return err == nil && until.After(now)
}
func relevantOccurrences(task Task, periods []Period, now time.Time) []Occurrence {
	today := localDay(now)
	var r []Occurrence
	for day := -2; day <= 1; day++ {
		target := today.AddDate(0, 0, day)
		os, e := OccurrencesForDate(task, target, periods)
		if e != nil {
			continue
		}
		for _, o := range os {
			actual := localDay(o.ScheduledAt)
			if sameDay(target, today) || sameDay(actual, today) {
				r = append(r, o)
			}
		}
	}
	return r
}
func dueSnoozedEvent(task Task, periods []Period, now time.Time) (Event, bool) {
	if task.State.SnoozeUntil == "" {
		return Event{}, false
	}
	s, e := time.Parse(time.RFC3339, task.State.SnoozeUntil)
	if e != nil || s.After(now) {
		return Event{}, false
	}
	o, ok := occurrenceForSnooze(task, periods, s)
	if !ok || eventAcknowledged(task, o) {
		return Event{}, false
	}
	return eventFromOccurrence(task, o, s), true
}
func occurrenceForSnooze(task Task, periods []Period, snooze time.Time) (Occurrence, bool) {
	var best Occurrence
	found := false
	base := localDay(snooze)
	for day := -3; day <= 1; day++ {
		cs, e := OccurrencesForDate(task, base.AddDate(0, 0, day), periods)
		if e != nil {
			continue
		}
		for _, c := range cs {
			if c.ScheduledAt.After(snooze) || eventAcknowledged(task, c) {
				continue
			}
			if !found || c.ScheduledAt.After(best.ScheduledAt) {
				best = c
				found = true
			}
		}
	}
	return best, found
}
func eventFromOccurrence(task Task, o Occurrence, dueAt time.Time) Event {
	return Event{TaskID: task.ID, TaskTitle: task.Title, Key: o.EventKey, TargetDate: o.TargetDate, ScheduledAt: o.ScheduledAt, DueAt: dueAt}
}
func eventAcknowledged(task Task, o Occurrence) bool {
	if task.State.LastFiredEvent == o.EventKey {
		return true
	}
	prefix := task.ID + ":"
	if !strings.HasPrefix(task.State.LastFiredEvent, prefix) || !strings.HasPrefix(o.EventKey, prefix) {
		return false
	}
	last := strings.TrimPrefix(task.State.LastFiredEvent, prefix)
	candidate := strings.TrimPrefix(o.EventKey, prefix)
	return last >= candidate
}
func localDay(v time.Time) time.Time {
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, v.Location())
}
func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

type NotificationQueue struct {
	pending []Event
	known   map[string]struct{}
}

func NewNotificationQueue() *NotificationQueue {
	return &NotificationQueue{known: make(map[string]struct{})}
}
func (q *NotificationQueue) Add(events ...Event) {
	for _, e := range events {
		if _, ok := q.known[e.Key]; ok {
			continue
		}
		q.known[e.Key] = struct{}{}
		q.pending = append(q.pending, e)
	}
}
func (q *NotificationQueue) Pop() (Event, bool) {
	if len(q.pending) == 0 {
		return Event{}, false
	}
	e := q.pending[0]
	q.pending = q.pending[1:]
	delete(q.known, e.Key)
	return e, true
}
func (q *NotificationQueue) RemoveTask(taskID string) {
	f := q.pending[:0]
	for _, e := range q.pending {
		if e.TaskID == taskID {
			delete(q.known, e.Key)
			continue
		}
		f = append(f, e)
	}
	q.pending = f
}
func (q *NotificationQueue) Len() int { return len(q.pending) }
