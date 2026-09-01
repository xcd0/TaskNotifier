//go:build windows

package tasknotifier

import (
	"errors"
	"fmt"
	"github.com/lxn/walk"
	"strings"
	"time"
)

func (app *App) savePeriodFromWeb(candidate Period) error {
	data := app.snapshot()
	candidate.Name = strings.TrimSpace(candidate.Name)
	candidate.StartTime = strings.TrimSpace(candidate.StartTime)
	candidate.EndTime = strings.TrimSpace(candidate.EndTime)
	if candidate.ID == "" {
		created, err := NewPeriod()
		if err != nil {
			return fmt.Errorf("期間IDを生成できません: %w", err)
		}
		candidate.ID = created.ID
		if err := candidate.Validate(); err != nil {
			return err
		}
		data.Periods = append(data.Periods, candidate)
	} else {
		index := periodIndex(data.Periods, candidate.ID)
		if index < 0 {
			return errors.New("編集対象の期間が見つかりません")
		}
		before := data.Periods[index]
		candidate.ID = before.ID
		if err := candidate.Validate(); err != nil {
			return err
		}
		data.Periods[index] = candidate
		if before.StartEnabled != candidate.StartEnabled || before.StartTime != candidate.StartTime || before.EndEnabled != candidate.EndEnabled || before.EndTime != candidate.EndTime {
			for taskIndex := range data.Tasks {
				if data.Tasks[taskIndex].Condition.PeriodEnabled && data.Tasks[taskIndex].Condition.PeriodID == candidate.ID {
					data.Tasks[taskIndex].State = State{}
				}
			}
		}
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("期間を保存できません: %w", err)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}

func (app *App) deletePeriodFromWeb(id string) error {
	data := app.snapshot()
	index := periodIndex(data.Periods, id)
	if index < 0 {
		return errors.New("削除対象の期間が見つかりません")
	}
	data.Periods = append(data.Periods[:index], data.Periods[index+1:]...)
	for taskIndex := range data.Tasks {
		if data.Tasks[taskIndex].Condition.PeriodID == id {
			data.Tasks[taskIndex].Condition = TaskCondition{}
			data.Tasks[taskIndex].State = State{}
		}
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("期間を削除できません: %w", err)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}

func (app *App) chooseBatchFile() (string, error) {
	dialog := walk.FileDialog{Title: "BATまたはCMDファイルを選択", Filter: "Batch files (*.bat;*.cmd)|*.bat;*.cmd|All files (*.*)|*.*"}
	selected, err := dialog.ShowOpen(app.mw)
	if err != nil {
		return "", fmt.Errorf("ファイルを選択できません: %w", err)
	}
	if !selected {
		return "", nil
	}
	return dialog.FilePath, nil
}
func periodIndex(periods []Period, id string) int {
	for index := range periods {
		if periods[index].ID == id {
			return index
		}
	}
	return -1
}
func formatHistoryTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.Local().Format("2006-01-02 15:04:05")
}
