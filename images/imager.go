package images

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type imagerServer struct {
	UnimplementedImagerServer
	image2ascii string // path to image2ascii binary
}

func NewImagerServer() ImagerServer {
	bin, err := exec.LookPath("image2ascii")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, "go", "bin", "image2ascii")
	}
	return &imagerServer{image2ascii: bin}
}

func (s *imagerServer) Render(req *RenderRequest, stream grpc.ServerStreamingServer[Frame]) error {
	if req.Source == "" {
		return status.Error(codes.InvalidArgument, "source is required")
	}

	// If remote URL, download to a temp file.
	source := req.Source
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		tmp, ext, err := downloadToTemp(source)
		if err != nil {
			return status.Errorf(codes.Internal, "download: %v", err)
		}
		defer os.Remove(tmp)
		source = tmp
		// If we couldn't get an extension from the URL, use the detected one.
		_ = ext
	}

	if _, err := os.Stat(source); err != nil {
		return status.Errorf(codes.NotFound, "source not found: %s", source)
	}

	opts := renderOpts{
		width:      int(req.Width),
		height:     int(req.Height),
		fps:        int(req.Fps),
		maxSeconds: req.MaxSeconds,
		color:      req.Color,
		invert:     req.Invert,
	}

	return s.renderFile(source, opts, stream)
}

func (s *imagerServer) RenderBytes(req *RenderBytesRequest, stream grpc.ServerStreamingServer[Frame]) error {
	if len(req.Data) == 0 {
		return status.Error(codes.InvalidArgument, "data is required")
	}

	ext := req.Format
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if ext == "" {
		ext = ".png"
	}

	tmp, err := os.CreateTemp("", "imager-*"+ext)
	if err != nil {
		return status.Errorf(codes.Internal, "tmpfile: %v", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(req.Data); err != nil {
		tmp.Close()
		return status.Errorf(codes.Internal, "write: %v", err)
	}
	tmp.Close()

	opts := renderOpts{
		width:      int(req.Width),
		height:     int(req.Height),
		fps:        int(req.Fps),
		maxSeconds: req.MaxSeconds,
		color:      req.Color,
		invert:     req.Invert,
	}

	return s.renderFile(tmp.Name(), opts, stream)
}

type renderOpts struct {
	width, height int
	fps           int
	maxSeconds    float32
	color, invert bool
}

// renderFile handles both static images and animated formats.
func (s *imagerServer) renderFile(path string, opts renderOpts, stream grpc.ServerStreamingServer[Frame]) error {
	if opts.width == 0 {
		opts.width = 80
	}

	ext := strings.ToLower(filepath.Ext(path))
	animated := ext == ".gif" || ext == ".webm" || ext == ".mp4" || ext == ".mov" || ext == ".webp"

	if !animated {
		// Static image: single frame.
		data, err := s.convertImage(path, opts)
		if err != nil {
			return err
		}
		return stream.Send(&Frame{Data: data, Index: 0, Total: 1})
	}

	// Animated: extract frames with ffmpeg, convert each.
	dir, err := os.MkdirTemp("", "imager-frames-*")
	if err != nil {
		return status.Errorf(codes.Internal, "tmpdir: %v", err)
	}
	defer os.RemoveAll(dir)

	fps := opts.fps
	if fps == 0 {
		fps = 10
	}

	ffArgs := []string{"-v", "quiet", "-i", path}
	vf := fmt.Sprintf("fps=%d", fps)
	if opts.maxSeconds > 0 {
		ffArgs = append(ffArgs, "-t", fmt.Sprintf("%.2f", opts.maxSeconds))
	}
	ffArgs = append(ffArgs, "-vf", vf, filepath.Join(dir, "frame_%04d.png"))

	cmd := exec.Command("ffmpeg", ffArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return status.Errorf(codes.Internal, "ffmpeg: %v: %s", err, out)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return status.Errorf(codes.Internal, "readdir: %v", err)
	}
	var framePaths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			framePaths = append(framePaths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(framePaths)

	total := int32(len(framePaths))
	for i, fp := range framePaths {
		data, err := s.convertImage(fp, opts)
		if err != nil {
			return err
		}
		if err := stream.Send(&Frame{Data: data, Index: int32(i), Total: total}); err != nil {
			return err
		}
	}
	return nil
}

// convertImage runs image2ascii on a single image file.
func (s *imagerServer) convertImage(path string, opts renderOpts) (string, error) {
	args := []string{"-f", path, "-w", fmt.Sprintf("%d", opts.width)}
	if opts.height > 0 {
		args = append(args, "-g", fmt.Sprintf("%d", opts.height))
	}
	if !opts.color {
		args = append(args, "-c=false")
	}
	if opts.invert {
		args = append(args, "-i")
	}

	out, err := exec.Command(s.image2ascii, args...).Output()
	if err != nil {
		return "", status.Errorf(codes.Internal, "image2ascii: %v", err)
	}
	return string(out), nil
}

// downloadToTemp fetches a URL and saves it to a temp file.
// Returns the temp path and detected extension.
func downloadToTemp(url string) (path, ext string, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Guess extension from Content-Type.
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "gif"):
		ext = ".gif"
	case strings.Contains(ct, "webm"):
		ext = ".webm"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	case strings.Contains(ct, "mp4"):
		ext = ".mp4"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		ext = ".jpg"
	default:
		ext = ".png"
	}

	// Also try from URL path.
	if ext == ".png" {
		urlExt := filepath.Ext(strings.SplitN(url, "?", 2)[0])
		if urlExt != "" {
			ext = urlExt
		}
	}

	tmp, err := os.CreateTemp("", "imager-dl-*"+ext)
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}

	return tmp.Name(), ext, nil
}
