package downloader

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// YtdlpFormat yt-dlp 格式元数据。
type YtdlpFormat struct {
	FormatID   string  `json:"format_id"`
	Ext        string  `json:"ext"`
	Vcodec     string  `json:"vcodec"`
	Acodec     string  `json:"acodec"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Fps        float64 `json:"fps"`
	Vbr        float64 `json:"vbr"`
	Abr        float64 `json:"abr"`
	Tbr        float64 `json:"tbr"`
	Asr        int     `json:"asr"`
	FormatNote string  `json:"format_note"`
}

// YtdlpInfo yt-dlp -J 输出。
type YtdlpInfo struct {
	Title            string        `json:"title"`
	ID               string        `json:"id"`
	Ext              string        `json:"ext"`
	Width            int           `json:"width"`
	Height           int           `json:"height"`
	Fps              float64       `json:"fps"`
	Tbr              float64       `json:"tbr"`
	Vcodec           string        `json:"vcodec"`
	Acodec           string        `json:"acodec"`
	Format           string        `json:"format"`
	Formats          []YtdlpFormat `json:"formats"`
	RequestedFormats []YtdlpFormat `json:"requested_formats"`
}

// youtubeBotBypassArgs YouTube 反爬墙备用客户端（原逻辑保留）。
var youtubeBotBypassArgs = []string{"--extractor-args", "youtube:player_client=tv,web_embedded,android_vr,android"}

// IsBotWall 判断是否反爬墙错误（原逻辑）。
func IsBotWall(stderr string) bool {
	return strings.Contains(stderr, "Sign in to confirm") || strings.Contains(stderr, "not a bot")
}

// appendPlatformArgs 追加平台参数（原逻辑）。
func appendPlatformArgs(args []string, cookieFile, platform string) []string {
	if info, ok := SupportedPlatforms[platform]; ok {
		if info.UserAgent != "" {
			args = append(args, "--user-agent", info.UserAgent)
		}
		if info.Referer != "" {
			args = append(args, "--add-header", "Referer: "+info.Referer)
		}
		args = append(args, info.ExtraArgs...)
	}
	if cookieFile != "" {
		args = append(args, "--cookies", cookieFile)
	}
	return args
}

// BuildParseCmd 构造解析命令（原逻辑）。
func BuildParseCmd(rawURL, cookieFile, platform string, extra ...string) []string {
	args := []string{
		YtdlpPath,
		"-J",
		"-f", UnifiedFormatSelector,
		"--format-sort", UnifiedFormatSort,
		"--no-playlist",
		"--no-warnings",
		"--no-progress",
		"--no-check-certificate",
	}
	args = appendPlatformArgs(args, cookieFile, platform)
	args = append(args, extra...)
	args = append(args, rawURL)
	return args
}

// BuildDownloadCmd 构造统一下载命令（原逻辑）。
func BuildDownloadCmd(rawURL, cookieFile, outputTemplate, platform string, extra ...string) []string {
	args := []string{
		YtdlpPath,
		"-f", UnifiedFormatSelector,
		"--format-sort", UnifiedFormatSort,
		"--merge-output-format", UnifiedMergeFormat,
		"--no-playlist",
		"--no-warnings",
		"--newline",
		"--no-mtime",
		"--no-part",
		"--no-check-certificate",
		"--concurrent-fragments", strconv.Itoa(ConcurrentFragments),
		"--ffmpeg-location", filepath.Dir(FfmpegPath),
		"--progress-template", "download:PROGRESS:%(progress._percent_str)s|%(progress._speed_str)s|%(progress._eta_str)s",
		"--progress-template", "postprocess:POSTPROC:%(postprocessor)s",
		"-o", outputTemplate,
	}
	args = appendPlatformArgs(args, cookieFile, platform)
	args = append(args, extra...)
	args = append(args, rawURL)
	return args
}

// BuildDirectDownloadCmd 直连下载命令（原逻辑）。
func BuildDirectDownloadCmd(directURL, outputTemplate, platform, ua string) []string {
	args := []string{
		YtdlpPath,
		"--no-playlist",
		"--no-warnings",
		"--newline",
		"--no-mtime",
		"--no-part",
		"--no-check-certificate",
		"--concurrent-fragments", strconv.Itoa(ConcurrentFragments),
		"--progress-template", "download:PROGRESS:%(progress._percent_str)s|%(progress._speed_str)s|%(progress._eta_str)s",
		"-o", outputTemplate,
	}
	if ua != "" {
		args = append(args, "--user-agent", ua)
	}
	if info, ok := SupportedPlatforms[platform]; ok && info.Referer != "" {
		args = append(args, "--add-header", "Referer: "+info.Referer)
	}
	args = append(args, directURL)
	return args
}

// ResolveBinaries 记录 yt-dlp / ffmpeg / ffprobe 路径（环境变量优先）。
func ResolveBinaries(ytdlp, ffmpeg, ffprobe string) {
	if ytdlp != "" {
		YtdlpPath = ytdlp
	}
	if ffmpeg != "" {
		FfmpegPath = ffmpeg
	}
	if ffprobe != "" {
		FfprobePath = ffprobe
	}
}

var (
	// YtdlpPath yt-dlp 可执行文件路径。
	YtdlpPath = "yt-dlp"
	// FfmpegPath ffmpeg 路径。
	FfmpegPath = "ffmpeg"
	// FfprobePath ffprobe 路径。
	FfprobePath = "ffprobe"
)

// RunCmd 执行命令并返回 stdout/stderr/退出码（原逻辑；命令不包含敏感参数）。
func RunCmd(args []string) (stdout, stderr string, rc int) {
	return RunCmdCtx(context.Background(), args)
}

// RunCmdCtx 带取消的简单命令执行。
func RunCmdCtx(ctx context.Context, args []string) (stdout, stderr string, rc int) {
	if len(args) == 0 {
		return "", "", -1
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Env = os.Environ()
	var so, se strings.Builder
	c.Stdout = &so
	c.Stderr = &se
	err := c.Run()
	rc = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			rc = -1
		}
	}
	return so.String(), se.String(), rc
}

// ProgressSink 进度回调（运行时任务更新 + 持久化钩子）。
type ProgressSink struct {
	OnStage    func(stage string, progress float64)
	OnProgress func(progress float64)
}

// RunCommandCtx 流式解析 yt-dlp 进度（原逻辑保留：分段映射、回落检测），支持取消。
// ctx 取消时终止命令；args 为完整命令（含 yt-dlp 路径）。
func RunCommandCtx(ctx context.Context, args []string, sink *ProgressSink, startPct, endPct float64, segments int, label string) error {
	if len(args) == 0 {
		return fmt.Errorf("空命令")
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Env = os.Environ()

	stdout, err := c.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	if err := c.Start(); err != nil {
		return fmt.Errorf("启动 yt-dlp 失败: %w", err)
	}

	var errLines []string
	var errMu sync.Mutex
	var stderrWg sync.WaitGroup
	stderrWg.Add(1)
	go func() {
		defer stderrWg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			errMu.Lock()
			errLines = append(errLines, sc.Text())
			if len(errLines) > 30 {
				errLines = errLines[len(errLines)-30:]
			}
			errMu.Unlock()
		}
	}()

	segSpan := (endPct - startPct) / float64(segments)
	curSeg := 0
	lastPct := -1.0
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PROGRESS:") {
			payload := strings.TrimPrefix(line, "PROGRESS:")
			parts := strings.Split(payload, "|")
			if len(parts) < 1 {
				continue
			}
			pctStr := strings.TrimSpace(parts[0])
			pctStr = strings.TrimSuffix(pctStr, "%")
			pct, perr := strconv.ParseFloat(pctStr, 64)
			if perr != nil {
				continue
			}
			if lastPct >= 0 && pct < lastPct-5 && curSeg < segments-1 {
				curSeg++
			}
			lastPct = pct

			speed, eta := "", ""
			if len(parts) > 1 {
				speed = strings.TrimSpace(parts[1])
			}
			if len(parts) > 2 {
				eta = strings.TrimSpace(parts[2])
			}
			segName := "视频"
			if segments > 1 && curSeg == 1 {
				segName = "音频"
			}
			if sink != nil && sink.OnStage != nil {
				sink.OnStage(fmt.Sprintf("[%s] %s流下载中 %.1f%% 速度:%s 剩余:%s", label, segName, pct, speed, eta), 0)
			}
			cur := startPct + float64(curSeg)*segSpan + segSpan*(pct/100.0)
			if cur < endPct {
				if sink != nil && sink.OnProgress != nil {
					sink.OnProgress(cur)
				}
			}
		} else if strings.HasPrefix(line, "POSTPROC:") {
			if sink != nil && sink.OnStage != nil {
				sink.OnStage("ffmpeg 合并音视频中...", endPct+1)
			}
		}
	}

	waitErr := c.Wait()
	stderrWg.Wait()

	if waitErr != nil {
		errMu.Lock()
		tail := strings.Join(errLines, "\n")
		errMu.Unlock()
		return fmt.Errorf("%s下载失败: %v\n输出:\n%s", label, waitErr, tail)
	}
	return nil
}

// FfmpegDir 返回 ffmpeg 目录。
func FfmpegDir() string {
	return filepath.Dir(FfmpegPath)
}


// isStoryboard 排除 storyboard 格式（原逻辑）。
func isStoryboard(f YtdlpFormat) bool {
	if strings.Contains(strings.ToLower(f.FormatNote), "storyboard") {
		return true
	}
	if strings.Contains(strings.ToLower(f.FormatID), "storyboard") {
		return true
	}
	if f.Vcodec != "none" && f.Vcodec != "" && f.Width <= 0 && f.Height <= 0 {
		return true
	}
	return false
}

// selectBestVideo 最高质量视频流：分辨率 → 帧率 → 码率（原逻辑）。
func selectBestVideo(formats []YtdlpFormat) *YtdlpFormat {
	var videoFmts []YtdlpFormat
	for _, f := range formats {
		if f.Vcodec == "none" || f.Vcodec == "" {
			continue
		}
		if isStoryboard(f) {
			continue
		}
		videoFmts = append(videoFmts, f)
	}
	if len(videoFmts) == 0 {
		return nil
	}
	sort.SliceStable(videoFmts, func(i, j int) bool {
		ri := videoFmts[i].Width * videoFmts[i].Height
		rj := videoFmts[j].Width * videoFmts[j].Height
		if ri != rj {
			return ri > rj
		}
		if videoFmts[i].Fps != videoFmts[j].Fps {
			return videoFmts[i].Fps > videoFmts[j].Fps
		}
		vi := videoFmts[i].Vbr
		if vi == 0 {
			vi = videoFmts[i].Tbr - videoFmts[i].Abr
		}
		vj := videoFmts[j].Vbr
		if vj == 0 {
			vj = videoFmts[j].Tbr - videoFmts[j].Abr
		}
		return vi > vj
	})
	return &videoFmts[0]
}

// selectBestAudio 最高音质音频流：码率 → 总码率 → 采样率（原逻辑）。
func selectBestAudio(formats []YtdlpFormat) *YtdlpFormat {
	var audioFmts []YtdlpFormat
	for _, f := range formats {
		if f.Vcodec == "none" && f.Acodec != "none" && f.Acodec != "" {
			audioFmts = append(audioFmts, f)
		}
	}
	if len(audioFmts) == 0 {
		return nil
	}
	sort.SliceStable(audioFmts, func(i, j int) bool {
		if audioFmts[i].Abr != audioFmts[j].Abr {
			return audioFmts[i].Abr > audioFmts[j].Abr
		}
		if audioFmts[i].Tbr != audioFmts[j].Tbr {
			return audioFmts[i].Tbr > audioFmts[j].Tbr
		}
		return audioFmts[i].Asr > audioFmts[j].Asr
	})
	return &audioFmts[0]
}
