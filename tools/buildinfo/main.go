package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexflint/go-arg"
)

type arguments struct {
	Version string `arg:"required,--version" help:"EXEへ埋め込んだ表示バージョン"`
	EXE     string `arg:"required,--exe" help:"ハッシュを計算するEXEのパス"`
	Changes string `arg:"required,--changes" help:"変更点テキストのパス"`
	Output  string `arg:"required,--output" help:"BUILD-INFO.txtの出力パス"`
}

func init() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)
}

func main() {
	var args arguments
	parser, err := arg.NewParser(arg.Config{Program: "buildinfo", IgnoreEnv: true}, &args)
	if err != nil {
		log.Fatal(err)
	}
	if err := parser.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
	if err := writeBuildInfo(args); err != nil {
		log.Fatal(err)
	}
}

// writeBuildInfo は配布EXEを直接読み、取り違えを検出できる情報をBOMなしUTF-8で保存する。
func writeBuildInfo(args arguments) error {
	executable, err := os.ReadFile(args.EXE)
	if err != nil {
		return fmt.Errorf("EXEを読み込めません: %w", err)
	}
	changes, err := os.ReadFile(args.Changes)
	if err != nil {
		return fmt.Errorf("変更点を読み込めません: %w", err)
	}
	hash := sha256.Sum256(executable)
	content := fmt.Sprintf(
		"TaskNotifier ビルド情報\n\nバージョン: %s\nEXE: %s\nEXE SHA-256: %x\n\n変更点:\n%s\n",
		strings.TrimSpace(args.Version),
		filepath.Base(args.EXE),
		hash,
		strings.TrimSpace(string(changes)),
	)
	if err := os.WriteFile(args.Output, []byte(content), 0o644); err != nil {
		return fmt.Errorf("ビルド情報を保存できません: %w", err)
	}
	return nil
}
