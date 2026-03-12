// generate: extracts frames from disk.webm and converts them to ASCII art,
// then writes a Go source file with the embedded frame data.
//
// Usage:
//
//	go run ./images/generate
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

const (
	fps     = 15  // frames per second to extract
	width   = 140 // ASCII art width in characters
	height  = 40  // ASCII art height (width/~3.5 for square source, since chars are ~2:1)
	maxSecs = 1   // keep 1 second for a quick splash
)

var tmpl = template.Must(template.New("animation").Parse(`package images

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	framesData = "{{.Data}}"
	frameFPS   = {{.FPS}}
)

// Frames decodes and returns all ASCII art frames.
func Frames() ([]string, error) {
	raw, err := base64.StdEncoding.DecodeString(framesData)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\x00"), nil
}

// Play prints the accretion disk animation.
func Play(loops int) error {
	frames, err := Frames()
	if err != nil {
		return err
	}

	// Clear screen once at the start.
	fmt.Fprint(os.Stdout, "\033[2J\033[H")

	frameDur := time.Second / time.Duration(frameFPS)

	for pass := 0; pass <= loops; pass++ {
		for _, frame := range frames {
			// Home cursor, print frame on top of previous.
			fmt.Fprint(os.Stdout, "\033[H")
			fmt.Fprint(os.Stdout, frame)
			time.Sleep(frameDur)
		}
	}

	// Leave some vertical room after the animation.
	fmt.Fprint(os.Stdout, "\n\n\n")
	return nil
}
`))

func main() {
	log.SetFlags(0)

	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	imagesDir := filepath.Join(root, "images")
	videoPath := filepath.Join(imagesDir, "disk.webm")
	framesDir := filepath.Join(imagesDir, "frames")

	if _, err := os.Stat(videoPath); err != nil {
		log.Fatalf("video not found: %s", videoPath)
	}

	os.RemoveAll(framesDir)
	os.MkdirAll(framesDir, 0755)
	defer os.RemoveAll(framesDir)

	image2ascii, err := exec.LookPath("image2ascii")
	if err != nil {
		home, _ := os.UserHomeDir()
		image2ascii = filepath.Join(home, "go", "bin", "image2ascii")
		if _, err := os.Stat(image2ascii); err != nil {
			log.Fatal("image2ascii not found; run: go install github.com/qeesung/image2ascii@latest")
		}
	}

	log.Printf("extracting frames at %d fps...", fps)
	cmd := exec.Command("ffmpeg", "-v", "quiet",
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%d", fps),
		filepath.Join(framesDir, "frame_%04d.png"),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("ffmpeg: %v", err)
	}

	entries, err := os.ReadDir(framesDir)
	if err != nil {
		log.Fatal(err)
	}
	var framePaths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			framePaths = append(framePaths, filepath.Join(framesDir, e.Name()))
		}
	}
	sort.Strings(framePaths)

	// Trim to maxSecs worth of frames for a snappy splash.
	maxFrames := fps * maxSecs
	if len(framePaths) > maxFrames {
		framePaths = framePaths[:maxFrames]
	}
	log.Printf("using %d frames (%.1fs)", len(framePaths), float64(len(framePaths))/float64(fps))

	var frames []string
	for i, fp := range framePaths {
		if i%20 == 0 {
			log.Printf("converting frame %d/%d...", i+1, len(framePaths))
		}
		out, err := exec.Command(image2ascii,
			"-f", fp,
			"-w", fmt.Sprintf("%d", width),
			"-g", fmt.Sprintf("%d", height),
		).Output()
		if err != nil {
			log.Fatalf("image2ascii frame %d: %v", i, err)
		}
		frames = append(frames, string(out))
	}

	log.Printf("converted %d frames, compressing...", len(frames))

	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	for i, f := range frames {
		if i > 0 {
			gz.Write([]byte("\x00"))
		}
		gz.Write([]byte(f))
	}
	gz.Close()

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	outPath := filepath.Join(imagesDir, "animation.go")
	var src bytes.Buffer
	if err := tmpl.Execute(&src, struct {
		Data  string
		Count int
		FPS   int
	}{encoded, len(frames), fps}); err != nil {
		log.Fatalf("template: %v", err)
	}

	if err := os.WriteFile(outPath, src.Bytes(), 0644); err != nil {
		log.Fatalf("write %s: %v", outPath, err)
	}
	log.Printf("wrote %s (%d frames, %.1f KB compressed)", outPath, len(frames), float64(len(encoded))/1024)
}
