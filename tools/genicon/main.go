package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"strings"

	"github.com/alexflint/go-arg"
)

const (
	// supersampling は小さいアイコンの輪郭を滑らかにする内部描画倍率である。
	supersampling = 4
	// designMarker はICOとSVGの意図しない取り違えを防ぐ識別子である。
	designMarker = `data-design="tasknotifier-icon-v2"`
)

type arguments struct {
	Source  string `arg:"--source" default:"resources/app.svg" help:"アイコンのSVG原本"`
	Output  string `arg:"--out" default:"resources/app.ico" help:"出力するICOファイル"`
	Preview string `arg:"--preview" help:"任意の256px PNGプレビュー出力先"`
	PWADir  string `arg:"--pwa-dir" help:"PWA用192px/512px PNGの出力ディレクトリ"`
}

type point struct {
	X float64
	Y float64
}

type iconDirectoryEntry struct {
	Width        byte
	Height       byte
	ColorCount   byte
	Reserved     byte
	Planes       uint16
	BitsPerPixel uint16
	DataSize     uint32
	DataOffset   uint32
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
	if err := generate(args); err != nil {
		log.Fatal(err)
	}
}

func parseArgs() (arguments, error) {
	var args arguments
	parser, err := arg.NewParser(arg.Config{Program: "genicon", IgnoreEnv: true}, &args)
	if err != nil {
		return arguments{}, fmt.Errorf("引数パーサーを作成できません: %w", err)
	}
	if err := parser.Parse(os.Args[1:]); err != nil {
		return arguments{}, err
	}
	return args, nil
}

func generate(args arguments) error {
	if err := validateVectorSource(args.Source); err != nil {
		return err
	}

	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, drawIcon(size)); err != nil {
			return fmt.Errorf("%dpx PNGを生成できません: %w", size, err)
		}
		images = append(images, encoded.Bytes())
	}

	if err := writeICO(args.Output, sizes, images); err != nil {
		return err
	}
	if strings.TrimSpace(args.Preview) != "" {
		if err := writePNG(args.Preview, drawIcon(256)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(args.PWADir) != "" {
		if err := os.MkdirAll(args.PWADir, 0o755); err != nil {
			return fmt.Errorf("PWAアイコン出力ディレクトリを作成できません: %w", err)
		}
		for _, size := range []int{192, 512} {
			path := fmt.Sprintf("%s/app-%d.png", strings.TrimRight(args.PWADir, `/\\`), size)
			if err := writePNG(path, drawIcon(size)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateVectorSource(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("SVG原本を読み込めません: %w", err)
	}
	if !bytes.Contains(source, []byte(designMarker)) {
		return fmt.Errorf("SVG原本のデザイン識別子が不正です: %s", path)
	}
	return nil
}

func writeICO(path string, sizes []int, images [][]byte) error {
	if len(sizes) != len(images) {
		return fmt.Errorf("ICOのサイズ数と画像数が一致しません")
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("ICOを作成できません: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if err := binary.Write(file, binary.LittleEndian, []uint16{0, 1, uint16(len(images))}); err != nil {
		return fmt.Errorf("ICOヘッダーを書き込めません: %w", err)
	}
	offset := uint32(6 + len(images)*16)
	for index, size := range sizes {
		dimension := byte(size)
		if size == 256 {
			dimension = 0
		}
		entry := iconDirectoryEntry{
			Width:        dimension,
			Height:       dimension,
			Planes:       1,
			BitsPerPixel: 32,
			DataSize:     uint32(len(images[index])),
			DataOffset:   offset,
		}
		if err := binary.Write(file, binary.LittleEndian, entry); err != nil {
			return fmt.Errorf("ICOディレクトリを書き込めません: %w", err)
		}
		offset += entry.DataSize
	}
	for _, encoded := range images {
		if _, err := file.Write(encoded); err != nil {
			return fmt.Errorf("ICO画像を書き込めません: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("ICOを同期できません: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("ICOを閉じられません: %w", err)
	}
	closed = true
	return nil
}

func writePNG(path string, source image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("PNGプレビューを作成できません: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if err := png.Encode(file, source); err != nil {
		return fmt.Errorf("PNGプレビューを書き込めません: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("PNGプレビューを閉じられません: %w", err)
	}
	closed = true
	return nil
}

func drawIcon(size int) image.Image {
	highSize := size * supersampling
	high := image.NewNRGBA(image.Rect(0, 0, highSize, highSize))
	bell := bellPolygon()

	for y := 0; y < highSize; y++ {
		for x := 0; x < highSize; x++ {
			vectorX := (float64(x) + 0.5) * 256 / float64(highSize)
			vectorY := (float64(y) + 0.5) * 256 / float64(highSize)
			pixel := vectorColor(vectorX, vectorY, bell)
			high.SetNRGBA(x, y, pixel)
		}
	}
	return downsample(high, size, supersampling)
}

func vectorColor(x, y float64, bell []point) color.NRGBA {
	if !insideRoundedRectangle(x, y, 12, 12, 232, 232, 56) {
		return color.NRGBA{}
	}

	progress := clamp(((x-36)+(y-22))/(188+216), 0, 1)
	pixel := mixColor(color.NRGBA{R: 67, G: 140, B: 245, A: 255}, color.NRGBA{R: 23, G: 77, B: 187, A: 255}, progress)

	if pointInPolygon(point{X: x, Y: y}, bell) || insideEllipse(x, y, 128, 218.5, 32, 11.5) {
		pixel = color.NRGBA{R: 248, G: 251, B: 255, A: 255}
	}
	if insideCircle(x, y, 184, 70, 31) {
		pixel = color.NRGBA{R: 248, G: 251, B: 255, A: 255}
	}
	if insideCircle(x, y, 184, 70, 23) {
		pixel = color.NRGBA{R: 42, G: 203, B: 130, A: 255}
	}
	return pixel
}

func bellPolygon() []point {
	points := []point{{X: 128, Y: 48}}
	points = appendCubic(points, point{X: 93, Y: 48}, point{X: 72, Y: 76}, point{X: 72, Y: 112})
	points = append(points, point{X: 72, Y: 145})
	points = appendCubic(points, point{X: 72, Y: 160}, point{X: 67, Y: 172}, point{X: 55, Y: 185})
	points = appendCubic(points, point{X: 51, Y: 190}, point{X: 55, Y: 198}, point{X: 62, Y: 198})
	points = append(points, point{X: 194, Y: 198})
	points = appendCubic(points, point{X: 201, Y: 198}, point{X: 205, Y: 190}, point{X: 201, Y: 185})
	points = appendCubic(points, point{X: 189, Y: 172}, point{X: 184, Y: 160}, point{X: 184, Y: 145})
	points = append(points, point{X: 184, Y: 112})
	points = appendCubic(points, point{X: 184, Y: 76}, point{X: 163, Y: 48}, point{X: 128, Y: 48})
	return points
}

func appendCubic(points []point, control1, control2, end point) []point {
	start := points[len(points)-1]
	const segments = 20
	for index := 1; index <= segments; index++ {
		t := float64(index) / segments
		oneMinusT := 1 - t
		points = append(points, point{
			X: oneMinusT*oneMinusT*oneMinusT*start.X + 3*oneMinusT*oneMinusT*t*control1.X + 3*oneMinusT*t*t*control2.X + t*t*t*end.X,
			Y: oneMinusT*oneMinusT*oneMinusT*start.Y + 3*oneMinusT*oneMinusT*t*control1.Y + 3*oneMinusT*t*t*control2.Y + t*t*t*end.Y,
		})
	}
	return points
}

func pointInPolygon(candidate point, polygon []point) bool {
	inside := false
	previous := polygon[len(polygon)-1]
	for _, current := range polygon {
		crosses := (current.Y > candidate.Y) != (previous.Y > candidate.Y)
		if crosses {
			intersectionX := (previous.X-current.X)*(candidate.Y-current.Y)/(previous.Y-current.Y) + current.X
			if candidate.X < intersectionX {
				inside = !inside
			}
		}
		previous = current
	}
	return inside
}

func insideRoundedRectangle(x, y, left, top, width, height, radius float64) bool {
	centerX := left + width/2
	centerY := top + height/2
	distanceX := math.Abs(x-centerX) - (width/2 - radius)
	distanceY := math.Abs(y-centerY) - (height/2 - radius)
	outside := math.Hypot(math.Max(distanceX, 0), math.Max(distanceY, 0))
	inside := math.Min(math.Max(distanceX, distanceY), 0)
	return outside+inside <= radius
}

func insideCircle(x, y, centerX, centerY, radius float64) bool {
	distanceX := x - centerX
	distanceY := y - centerY
	return distanceX*distanceX+distanceY*distanceY <= radius*radius
}

func insideEllipse(x, y, centerX, centerY, radiusX, radiusY float64) bool {
	distanceX := (x - centerX) / radiusX
	distanceY := (y - centerY) / radiusY
	return distanceX*distanceX+distanceY*distanceY <= 1
}

func mixColor(start, end color.NRGBA, progress float64) color.NRGBA {
	mix := func(left, right uint8) uint8 {
		return uint8(math.Round(float64(left)*(1-progress) + float64(right)*progress))
	}
	return color.NRGBA{R: mix(start.R, end.R), G: mix(start.G, end.G), B: mix(start.B, end.B), A: mix(start.A, end.A)}
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(math.Max(value, minimum), maximum)
}

func downsample(source *image.NRGBA, size, factor int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var alphaTotal uint64
			var redTotal uint64
			var greenTotal uint64
			var blueTotal uint64
			for sampleY := 0; sampleY < factor; sampleY++ {
				for sampleX := 0; sampleX < factor; sampleX++ {
					pixel := source.NRGBAAt(x*factor+sampleX, y*factor+sampleY)
					alpha := uint64(pixel.A)
					alphaTotal += alpha
					redTotal += uint64(pixel.R) * alpha
					greenTotal += uint64(pixel.G) * alpha
					blueTotal += uint64(pixel.B) * alpha
				}
			}
			samples := uint64(factor * factor)
			if alphaTotal == 0 {
				continue
			}
			result.SetNRGBA(x, y, color.NRGBA{
				R: uint8(redTotal / alphaTotal),
				G: uint8(greenTotal / alphaTotal),
				B: uint8(blueTotal / alphaTotal),
				A: uint8(alphaTotal / samples),
			})
		}
	}
	return result
}
