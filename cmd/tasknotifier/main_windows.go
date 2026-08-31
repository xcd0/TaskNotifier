//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/alexflint/go-arg"
	"github.com/lxn/walk"

	"tasknotifier/internal/tasknotifier"
)

type arguments struct {
	Background bool `arg:"--background" help:"メイン画面を表示せずタスクトレイへ常駐する"`
}

func init() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)
}

func main() {
	args, err := ParseArgs()
	if err != nil {
		showFatal("起動オプションが不正です", err)
		return
	}

	guard, alreadyRunning, err := tasknotifier.AcquireSingleInstance()
	if err != nil {
		showFatal("TaskNotifierを起動できません", err)
		return
	}
	if alreadyRunning {
		tasknotifier.RaiseExistingWindow()
		return
	}
	defer guard.Close()

	executable, err := os.Executable()
	if err != nil {
		showFatal("EXEの場所を確認できません", err)
		return
	}
	paths, err := tasknotifier.ResolvePaths(executable)
	if err != nil {
		showFatal("実行時パスを解決できません", err)
		return
	}
	paths.Log = filepath.Join(filepath.Dir(paths.Tasks), "logs", "TaskNotifier.log")
	logFile, logPath, logErr := tasknotifier.StartDiagnosticLogging(paths.Log)
	if logErr != nil {
		walk.MsgBox(nil, "診断ログを作成できません", logErr.Error(), walk.MsgBoxOK|walk.MsgBoxIconWarning)
	} else {
		defer logFile.Close()
		paths.Log = logPath
	}
	log.Printf("application start executable=%q background=%t", paths.Executable, args.Background)
	log.Printf("application paths tasks=%q log=%q legacy=%q", paths.Tasks, paths.Log, paths.LegacyTasks)
	migrated, err := tasknotifier.MigrateLegacyTaskFile(paths)
	if err != nil {
		showFatal("既存のタスク設定をAppDataへ移行できません", err)
		return
	}
	if migrated {
		log.Printf("既存のタスク設定をAppDataへ移行しました。")
	}
	store := tasknotifier.NewStore(paths.Tasks)
	log.Printf("application state path=%q", store.StatePath())
	if err := store.RecoverTemporary(); err != nil {
		log.Printf("一時ファイルの復旧: %v", err)
	}
	data, stamp, err := store.Load()
	startupError := ""
	if err != nil {
		startupError = "tasks.jsonを読み込めません: " + err.Error()
		log.Printf("%s", startupError)
		walk.MsgBox(nil, "tasks.jsonを読み込めません", "内容は変更していません。GUIでtasks.jsonを開いて修正できます。\n\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		data = tasknotifier.EmptyTaskFile()
		stamp = tasknotifier.FileStamp{}
	} else {
		changed, normalizeErr := normalizeBatchPaths(&data, paths.Directory)
		if normalizeErr != nil {
			startupError = "BATパスを絶対パスへ移行できません: " + normalizeErr.Error()
			log.Printf("%s", startupError)
		} else if changed {
			stamp, err = store.Save(data)
			if err != nil {
				startupError = "BATパスを絶対パスへ保存できません: " + err.Error()
				log.Printf("%s", startupError)
			} else {
				log.Printf("BAT/CMDパスを絶対パスへ移行しました。")
			}
		}
	}
	if err := tasknotifier.RunApp(paths, store, data, stamp, args.Background, startupError); err != nil {
		showFatal("TaskNotifierを開始できません", err)
	}
}

// ParseArgs はGUI初期化より先にコマンドラインを解析する。
func ParseArgs() (arguments, error) {
	var args arguments
	parser, err := arg.NewParser(arg.Config{Program: "TaskNotifier", IgnoreEnv: true}, &args)
	if err != nil {
		return arguments{}, fmt.Errorf("引数パーサーを作成できません: %w", err)
	}
	if err := parser.Parse(os.Args[1:]); err != nil {
		return arguments{}, err
	}
	return args, nil
}

func normalizeBatchPaths(data *tasknotifier.TaskFile, executableDirectory string) (bool, error) {
	changed := false
	for index := range data.Tasks {
		if data.Tasks[index].Action.BatPath == "" {
			continue
		}
		normalized, err := tasknotifier.NormalizeBATPath(executableDirectory, data.Tasks[index].Action.BatPath)
		if err != nil {
			return false, fmt.Errorf("%q: %w", data.Tasks[index].Title, err)
		}
		if normalized != data.Tasks[index].Action.BatPath {
			data.Tasks[index].Action.BatPath = normalized
			changed = true
		}
	}
	return changed, nil
}

func showFatal(title string, err error) {
	log.Printf("%s: %v", title, err)
	walk.MsgBox(nil, title, err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
}
