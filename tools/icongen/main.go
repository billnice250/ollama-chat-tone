package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

var pngSizes = []int{16, 32, 64, 128, 256, 512, 1024}

var iconsetFiles = map[string]int{
	"icon_16x16.png":     16,
	"icon_16x16@2x.png":  32,
	"icon_32x32.png":     32,
	"icon_32x32@2x.png":  64,
	"icon_128x128.png":   128,
	"icon_128x128@2x.png": 256,
	"icon_256x256.png":   256,
	"icon_256x256@2x.png": 512,
	"icon_512x512.png":   512,
	"icon_512x512@2x.png": 1024,
}

func main() {
	if err := os.MkdirAll("assets/logo.iconset", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll("assets", 0o755); err != nil {
		log.Fatal(err)
	}

	pngs := map[int][]byte{}
	for _, size := range pngSizes {
		data := renderPNG(size)
		pngs[size] = data
	}
	for name, size := range iconsetFiles {
		if err := os.WriteFile(filepath.Join("assets/logo.iconset", name), pngs[size], 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeICO("assets/logo.ico", [][]byte{pngs[16], pngs[32], pngs[64], pngs[256]}, []int{16, 32, 64, 256}); err != nil {
		log.Fatal(err)
	}
	if err := writeICNS("assets/logo.icns", map[string][]byte{
		"icp4": pngs[16],
		"icp5": pngs[32],
		"icp6": pngs[64],
		"ic07": pngs[128],
		"ic08": pngs[256],
		"ic09": pngs[512],
		"ic10": pngs[1024],
	}); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("assets/logo.png", pngs[512], 0o644); err != nil {
		log.Fatal(err)
	}
}

func renderPNG(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawRoundedRect(img, 0, 0, size, size, float64(size)*0.22, color.RGBA{7, 11, 19, 255})

	scale := float64(size) / 128
	drawCircle(img, 42*scale, 38*scale, 7*scale, color.RGBA{129, 140, 248, 255})
	drawCircle(img, 86*scale, 38*scale, 7*scale, color.RGBA{167, 139, 250, 255})

	drawRoundedRect(img, int(31*scale), int(39*scale), int(97*scale), int(92*scale), 14*scale, color.RGBA{17, 24, 39, 255})
	drawLine(img, 50*scale, 64*scale, 78*scale, 64*scale, 7*scale, color.RGBA{229, 231, 235, 255})
	drawLine(img, 50*scale, 78*scale, 68*scale, 78*scale, 7*scale, color.RGBA{229, 231, 235, 255})
	drawLine(img, 87*scale, 80*scale, 100*scale, 93*scale, 7*scale, color.RGBA{167, 139, 250, 255})
	drawLine(img, 100*scale, 80*scale, 87*scale, 93*scale, 7*scale, color.RGBA{167, 139, 250, 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

func drawRoundedRect(img *image.RGBA, x0, y0, x1, y1 int, radius float64, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dx := math.Max(math.Max(float64(x0)-float64(x), 0), float64(x)-float64(x1-1))
			dy := math.Max(math.Max(float64(y0)-float64(y), 0), float64(y)-float64(y1-1))
			if dx == 0 && dy == 0 {
				img.SetRGBA(x, y, c)
				continue
			}
			if math.Hypot(dx, dy) <= radius {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func drawCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	for y := int(cy - r); y <= int(cy+r); y++ {
		for x := int(cx - r); x <= int(cx+r); x++ {
			if x >= 0 && y >= 0 && x < img.Bounds().Dx() && y < img.Bounds().Dy() && math.Hypot(float64(x)-cx, float64(y)-cy) <= r {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, width float64, c color.RGBA) {
	minX := int(math.Min(x0, x1) - width)
	maxX := int(math.Max(x0, x1) + width)
	minY := int(math.Min(y0, y1) - width)
	maxY := int(math.Max(y0, y1) + width)
	length := math.Hypot(x1-x0, y1-y0)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			t := ((float64(x)-x0)*(x1-x0) + (float64(y)-y0)*(y1-y0)) / (length * length)
			t = math.Max(0, math.Min(1, t))
			px := x0 + t*(x1-x0)
			py := y0 + t*(y1-y0)
			if math.Hypot(float64(x)-px, float64(y)-py) <= width/2 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func writeICO(path string, images [][]byte, imageSizes []int) error {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(images)))

	offset := 6 + len(images)*16
	for i, data := range images {
		size := imageSizes[i]
		if size == 256 {
			size = 0
		}
		buf.WriteByte(byte(size))
		buf.WriteByte(byte(size))
		buf.WriteByte(0)
		buf.WriteByte(0)
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, data := range images {
		buf.Write(data)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeICNS(path string, images map[string][]byte) error {
	order := []string{"icp4", "icp5", "icp6", "ic07", "ic08", "ic09", "ic10"}
	total := uint32(8)
	for _, typ := range order {
		total += uint32(8 + len(images[typ]))
	}

	var buf bytes.Buffer
	buf.WriteString("icns")
	_ = binary.Write(&buf, binary.BigEndian, total)
	for _, typ := range order {
		data := images[typ]
		buf.WriteString(typ)
		_ = binary.Write(&buf, binary.BigEndian, uint32(8+len(data)))
		buf.Write(data)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
