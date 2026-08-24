package tasknotifier

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	FormatVersion      = 1
	TaskFileName       = "tasks.json"
	LegacyTaskFileName = "tasks.txt"
	ScheduleDailyFixed       = "daily_fixed"
	ScheduleDailyRandomAfter = "daily_random_after"
	ScheduleDailyBefore      = "daily_before"
	NotificationDialog = "dialog"
	NotificationOS     = "os"
	TaskKindNotification = "notification"
	TaskKindTodo         = "todo"
	HistoryLimit = 1000
)

type TaskFile struct {
	FormatVersion int `json:"format_version"`
	Periods []Period `json:"periods"`
	Tasks []Task `json:"tasks"`
	History []HistoryEntry `json:"history"`
}

type Task struct {
	ID string `json:"id"`
	Kind string `json:"kind,omitempty"`
	Enabled bool `json:"enabled"`
	Title string `json:"title"`
	Schedule Schedule `json:"schedule"`
	Condition TaskCondition `json:"condition"`
	Notification NotificationSettings `json:"notification"`
	Action TaskAction `json:"action"`
	State State `json:"state"`
}

type Schedule struct { Type string `json:"type"`; Time string `json:"time"`; Minutes int `json:"minutes"`; RepeatEnabled bool `json:"repeat_enabled"`; IntervalMinutes int `json:"interval_minutes"`; EndEnabled bool `json:"end_enabled"`; EndTime string `json:"end_time"` }
type Period struct { ID string `json:"id"`; Name string `json:"name"`; StartEnabled bool `json:"start_enabled"`; StartTime string `json:"start_time"`; EndEnabled bool `json:"end_enabled"`; EndTime string `json:"end_time"` }
type TaskCondition struct { PeriodEnabled bool `json:"period_enabled"`; PeriodID string `json:"period_id"`; WeekdaysEnabled bool `json:"weekdays_enabled"`; Weekdays []string `json:"weekdays,omitempty"` }
type NotificationSettings struct { Enabled *bool `json:"enabled,omitempty"`; Method string `json:"method"` }
type HistoryEntry struct { EventKey string `json:"event_key"`; TaskID string `json:"task_id"`; TaskTitle string `json:"task_title"`; ScheduledAt string `json:"scheduled_at"`; NotifiedAt string `json:"notified_at"`; Method string `json:"method"`; Result string `json:"result"` }
type TaskAction struct { BatPath string `json:"bat_path"`; ShowConsole bool `json:"show_console"` }
type State struct { LastFiredEvent string `json:"last_fired_event"`; SnoozeUntil string `json:"snooze_until"`; Completed bool `json:"completed,omitempty"`; CompletedAt string `json:"completed_at,omitempty"` }

func EmptyTaskFile() TaskFile { return TaskFile{FormatVersion: FormatVersion, Periods: DefaultPeriods(), Tasks: []Task{}, History: []HistoryEntry{}} }
func DefaultPeriods() []Period { return []Period{{ID:"preparation",Name:"準備時間",EndEnabled:true,EndTime:"09:00"},{ID:"work",Name:"勤務時間",StartEnabled:true,StartTime:"09:00",EndEnabled:true,EndTime:"18:00"},{ID:"overtime",Name:"残業時間",StartEnabled:true,StartTime:"18:00"}} }
func NewTask() (Task,error) { id,err:=NewTaskID(); if err!=nil{return Task{},err}; return Task{ID:id,Kind:TaskKindNotification,Enabled:true,Notification:NotificationSettings{Enabled:boolPointer(true),Method:NotificationOS},Schedule:Schedule{Type:ScheduleDailyFixed,Time:"09:00",IntervalMinutes:60}},nil }
func NewPeriod() (Period,error) { id,err:=NewTaskID(); if err!=nil{return Period{},err}; return Period{ID:id,Name:"新しい期間"},nil }
func NewTaskID() (string,error) { var value [16]byte; if _,err:=rand.Read(value[:]);err!=nil{return "",fmt.Errorf("タスクIDを生成できません: %w",err)}; return hex.EncodeToString(value[:]),nil }

func (f TaskFile) Validate() error {
	if f.FormatVersion!=FormatVersion{return fmt.Errorf("未対応のformat_versionです: %d",f.FormatVersion)}
	periodIDs:=make(map[string]struct{},len(f.Periods)); for i:=range f.Periods { if err:=f.Periods[i].Validate();err!=nil{return fmt.Errorf("periods[%d]: %w",i,err)}; if _,ok:=periodIDs[f.Periods[i].ID];ok{return fmt.Errorf("periods[%d]: idが重複しています: %s",i,f.Periods[i].ID)}; periodIDs[f.Periods[i].ID]=struct{}{} }
	ids:=make(map[string]struct{},len(f.Tasks)); for i:=range f.Tasks { if err:=f.Tasks[i].Validate();err!=nil{return fmt.Errorf("tasks[%d]: %w",i,err)}; if _,ok:=ids[f.Tasks[i].ID];ok{return fmt.Errorf("tasks[%d]: idが重複しています: %s",i,f.Tasks[i].ID)}; ids[f.Tasks[i].ID]=struct{}{}; if f.Tasks[i].Condition.PeriodEnabled { if _,ok:=periodIDs[f.Tasks[i].Condition.PeriodID];!ok{return fmt.Errorf("tasks[%d]: 期間条件のIDが存在しません: %s",i,f.Tasks[i].Condition.PeriodID)} } }
	return nil
}

func (t Task) Validate() error {
	if strings.TrimSpace(t.ID)==""{return errors.New("idが空です")}; if strings.TrimSpace(t.Title)==""{return errors.New("titleが空です")}
	kind:=t.Kind; if kind==""{kind=TaskKindNotification}; if kind!=TaskKindNotification&&kind!=TaskKindTodo{return fmt.Errorf("未対応のtask.kindです: %q",t.Kind)}
	if kind==TaskKindTodo { if t.State.CompletedAt!="" { if _,err:=time.Parse(time.RFC3339,t.State.CompletedAt);err!=nil{return fmt.Errorf("完了日時が不正です: %w",err)} }; return nil }
	if _,_,err:=ParseClock(t.Schedule.Time);err!=nil{return err}
	switch t.Schedule.Type { case ScheduleDailyFixed: if t.Schedule.Minutes!=0{return errors.New("daily_fixedのminutesは0である必要があります")}; case ScheduleDailyRandomAfter: if t.Schedule.Minutes<0||t.Schedule.Minutes>30{return errors.New("daily_random_afterのminutesは0から30の範囲です")}; case ScheduleDailyBefore: if t.Schedule.Minutes<0||t.Schedule.Minutes>1440{return errors.New("daily_beforeのminutesは0から1440の範囲です")}; default:return fmt.Errorf("未対応のschedule.typeです: %q",t.Schedule.Type) }
	if t.Schedule.RepeatEnabled&&(t.Schedule.IntervalMinutes<1||t.Schedule.IntervalMinutes>1440){return errors.New("通知間隔は1から1440分の範囲です")}
	if t.Schedule.EndEnabled { if !t.Schedule.RepeatEnabled{return errors.New("通知終了時刻は通知間隔がONのときだけ設定できます")}; if _,_,err:=ParseClock(t.Schedule.EndTime);err!=nil{return fmt.Errorf("通知終了時刻: %w",err)} }
	if t.Condition.PeriodEnabled&&strings.TrimSpace(t.Condition.PeriodID)==""{return errors.New("期間条件がONですが期間が選択されていません")}
	if t.Condition.WeekdaysEnabled { if len(t.Condition.Weekdays)==0{return errors.New("曜日条件がONですが曜日が選択されていません")}; seen:=map[string]struct{}{}; for _,w:=range t.Condition.Weekdays { if !ValidWeekdayKey(w){return fmt.Errorf("曜日条件に未対応の値があります: %q",w)}; if _,ok:=seen[w];ok{return fmt.Errorf("曜日条件が重複しています: %q",w)}; seen[w]=struct{}{} } }
	if t.Notification.Method!=""&&t.Notification.Method!=NotificationDialog&&t.Notification.Method!=NotificationOS{return fmt.Errorf("未対応の通知方法です: %q",t.Notification.Method)}
	if t.State.SnoozeUntil!="" { if _,err:=time.Parse(time.RFC3339,t.State.SnoozeUntil);err!=nil{return fmt.Errorf("snooze_untilがRFC3339形式ではありません: %w",err)} }
	return nil
}

func (p Period) Validate() error { if strings.TrimSpace(p.ID)==""{return errors.New("idが空です")}; if strings.TrimSpace(p.Name)==""{return errors.New("nameが空です")}; if p.StartEnabled { if _,_,err:=ParseClock(p.StartTime);err!=nil{return fmt.Errorf("開始時刻: %w",err)} }; if p.EndEnabled { if _,_,err:=ParseClock(p.EndTime);err!=nil{return fmt.Errorf("終了時刻: %w",err)} }; return nil }
func (p Period) Contains(value time.Time) bool { minuteOfDay:=value.Hour()*60+value.Minute(); start:=0; end:=24*60; if p.StartEnabled { h,m,e:=ParseClock(p.StartTime);if e!=nil{return false};start=h*60+m }; if p.EndEnabled { h,m,e:=ParseClock(p.EndTime);if e!=nil{return false};end=h*60+m }; if p.StartEnabled&&p.EndEnabled&&end<=start{return minuteOfDay>=start||minuteOfDay<end}; return minuteOfDay>=start&&minuteOfDay<end }
func ParseClock(value string)(hour,minute int,err error){p,e:=time.Parse("15:04",value);if e!=nil{return 0,0,fmt.Errorf("timeはHH:MM形式で指定してください: %q",value)};return p.Hour(),p.Minute(),nil}
func ScheduleChanged(before,after Schedule)bool{return before!=after}
func ApplyEdit(before,edited Task)Task{edited.ID=before.ID;bk:=before.Kind;if bk==""{bk=TaskKindNotification};ek:=edited.Kind;if ek==""{ek=TaskKindNotification};if bk!=ek||ScheduleChanged(before.Schedule,edited.Schedule)||ConditionChanged(before.Condition,edited.Condition){edited.State=State{}}else{edited.State=before.State};return edited}
func ConditionChanged(before,after TaskCondition)bool{if before.PeriodEnabled!=after.PeriodEnabled||before.WeekdaysEnabled!=after.WeekdaysEnabled{return true};if before.PeriodEnabled&&before.PeriodID!=after.PeriodID{return true};if !before.WeekdaysEnabled{return false};if len(before.Weekdays)!=len(after.Weekdays){return true};s:=map[string]struct{}{};for _,w:=range before.Weekdays{s[w]=struct{}{}};for _,w:=range after.Weekdays{if _,ok:=s[w];!ok{return true}};return false}
func ValidWeekdayKey(v string)bool{switch v{case "sun","mon","tue","wed","thu","fri","sat":return true};return false}
func WeekdayKey(v time.Weekday)string{return []string{"sun","mon","tue","wed","thu","fri","sat"}[int(v)]}
func FormatWeekdays(ws []string)string{if len(ws)==0{return "指定なし"};s:=map[string]struct{}{};for _,w:=range ws{s[w]=struct{}{}};order:=[]struct{Key,Label string}{{"mon","月"},{"tue","火"},{"wed","水"},{"thu","木"},{"fri","金"},{"sat","土"},{"sun","日"}};r:=[]string{};for _,w:=range order{if _,ok:=s[w.Key];ok{r=append(r,w.Label)}};return strings.Join(r,"・")}
func FormatSchedule(s Schedule)string{b:="";switch s.Type{case ScheduleDailyFixed:b=fmt.Sprintf("毎日 %s",s.Time);case ScheduleDailyRandomAfter:b=fmt.Sprintf("毎日 %sから0〜%d分後",s.Time,s.Minutes);case ScheduleDailyBefore:b=fmt.Sprintf("毎日 %sの%d分前",s.Time,s.Minutes);default:return "未対応: "+s.Type};if s.RepeatEnabled{b+=fmt.Sprintf(" / %d分間隔",s.IntervalMinutes);if s.EndEnabled{b+=" / "+s.EndTime+"まで"}};return b}
func FormatPeriod(p Period)string{s:="開始なし";e:="終了なし";if p.StartEnabled{s=p.StartTime};if p.EndEnabled{e=p.EndTime};return s+" - "+e}
func PeriodByID(ps []Period,id string)(Period,bool){for _,p:=range ps{if p.ID==id{return p,true}};return Period{},false}
func CurrentPeriodNames(ps []Period,now time.Time)[]string{var n []string;for _,p:=range ps{if p.Contains(now){n=append(n,p.Name)}};return n}
func NotificationEnabled(t Task)bool{return t.Notification.Enabled==nil||*t.Notification.Enabled}
func boolPointer(v bool)*bool{return &v}
func EffectiveNotificationMethod(t Task)string{if t.Notification.Method==NotificationOS{return NotificationOS};return NotificationDialog}
func AppendHistory(d TaskFile,e HistoryEntry)TaskFile{d=cloneTaskFile(d);d.History=append(d.History,e);if len(d.History)>HistoryLimit{d.History=append([]HistoryEntry(nil),d.History[len(d.History)-HistoryLimit:]...)};return d}

type Paths struct{Executable,Directory,Tasks,LegacyTasks,Log string}
func ResolvePaths(executable string)(Paths,error){a,e:=filepath.Abs(executable);if e!=nil{return Paths{},fmt.Errorf("EXEパスを解決できません: %w",e)};a=filepath.Clean(a);d:=filepath.Dir(a);return Paths{Executable:a,Directory:d,Tasks:filepath.Join(d,TaskFileName),LegacyTasks:filepath.Join(d,LegacyTaskFileName),Log:filepath.Join(d,"TaskNotifier.log")},nil}
func ResolveBATPath(directory,configured string)string{configured=strings.TrimSpace(configured);if configured==""{return ""};if filepath.IsAbs(configured){return filepath.Clean(configured)};return filepath.Clean(filepath.Join(directory,configured))}
