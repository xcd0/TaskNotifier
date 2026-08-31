package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexflint/go-arg"
	"github.com/evanw/esbuild/pkg/api"
)

const (
	styleMarker   = "/* TASKNOTIFIER_STYLE */"
	scriptMarker  = "/* TASKNOTIFIER_SCRIPT */"
	pwaHeadMarker = "<!-- TASKNOTIFIER_PWA_HEAD -->"
)

type arguments struct {
	Source    string `arg:"--source" default:"web" help:"Web UIソースディレクトリ"`
	Output    string `arg:"--output" default:"internal/tasknotifier/webui_dist/index.html" help:"EXE埋め込みHTMLの出力先"`
	PWAOutput string `arg:"--pwa-output" default:"dist/pwa" help:"PWA出力ディレクトリ"`
}

func init() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)
}

func main() {
	args, err := parseArgs()
	if err != nil {
		log.Fatal(err)
	}
	if err := buildWebUI(args); err != nil {
		log.Fatal(err)
	}
}

// parseArgs はビルド用パスだけをコマンドラインから受け取る。
func parseArgs() (arguments, error) {
	var args arguments
	parser, err := arg.NewParser(arg.Config{Program: "webbuild", IgnoreEnv: true}, &args)
	if err != nil {
		return arguments{}, fmt.Errorf("引数パーサーを作成できません: %w", err)
	}
	if err := parser.Parse(os.Args[1:]); err != nil {
		return arguments{}, err
	}
	return args, nil
}

// buildWebUI は共通Web UIからEXE埋め込み版とPWA版を生成する。
func buildWebUI(args arguments) error {
	templatePath := filepath.Join(args.Source, "index.html")
	stylePath := filepath.Join(args.Source, "styles.css")
	scriptPath := filepath.Join(args.Source, "app.ts")

	template, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("HTMLテンプレートを読み込めません: %w", err)
	}
	if strings.Count(string(template), styleMarker) != 1 || strings.Count(string(template), scriptMarker) != 1 || strings.Count(string(template), pwaHeadMarker) != 1 {
		return fmt.Errorf("HTMLテンプレートの埋め込み位置が不正です")
	}

	style, err := buildStyle(stylePath)
	if err != nil {
		return err
	}
	script, err := buildScript(scriptPath)
	if err != nil {
		return err
	}

	base := strings.Replace(string(template), styleMarker, style, 1)
	base = strings.Replace(base, scriptMarker, strings.ReplaceAll(script, "</script", "<\\/script"), 1)

	exeHTML := strings.Replace(base, pwaHeadMarker, "", 1)
	if err := writeFile(args.Output, []byte(exeHTML)); err != nil {
		return fmt.Errorf("EXE用埋め込みHTMLを保存できません: %w", err)
	}

	pwaHead := `<link rel="manifest" href="./manifest.webmanifest"><meta name="theme-color" content="#ffffff"><link rel="icon" href="./icons/app-192.png">`
	pwaHTML := strings.Replace(base, pwaHeadMarker, pwaHead, 1)
	if err := writeFile(filepath.Join(args.PWAOutput, "index.html"), []byte(pwaHTML)); err != nil {
		return fmt.Errorf("PWA HTMLを保存できません: %w", err)
	}
	for _, relative := range []string{"manifest.webmanifest", "service-worker.js", filepath.Join("icons", "app-192.png"), filepath.Join("icons", "app-512.png")} {
		if err := copyFile(filepath.Join(args.Source, "pwa", relative), filepath.Join(args.PWAOutput, relative)); err != nil {
			return err
		}
	}
	return nil
}

// writeFile は親ディレクトリを作成してファイルを保存する。
func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// copyFile はPWAの静的アセットをビルド出力へコピーする。
func copyFile(source, destination string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("PWAアセットを読み込めません: %w", err)
	}
	if err := writeFile(destination, content); err != nil {
		return fmt.Errorf("PWAアセットを保存できません: %w", err)
	}
	return nil
}

// buildStyle はCSSを検証し、余分な空白を除いて返す。
func buildStyle(path string) (string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("CSSを読み込めません: %w", err)
	}
	result := api.Transform(string(source), api.TransformOptions{
		Loader:           api.LoaderCSS,
		Target:           api.ES2020,
		MinifySyntax:     true,
		MinifyWhitespace: true,
		LogLevel:         api.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("CSSを処理できません: %s", result.Errors[0].Text)
	}
	return string(result.Code), nil
}

// buildScript はTypeScriptを単一のブラウザー用JavaScriptへ変換する。
func buildScript(path string) (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{path},
		Bundle:            true,
		Write:             false,
		Target:            api.ES2020,
		Format:            api.FormatIIFE,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		MinifyWhitespace:  true,
		LogLevel:          api.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("TypeScriptを処理できません: %s", result.Errors[0].Text)
	}
	if len(result.OutputFiles) != 1 {
		return "", fmt.Errorf("JavaScript出力数が不正です: %d", len(result.OutputFiles))
	}
	return string(result.OutputFiles[0].Contents), nil
}
