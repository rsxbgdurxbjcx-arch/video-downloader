package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// directVideo 自研解析结果。
type directVideo struct {
	URL     string
	Title   string
	Width   int
	Height  int
	Quality string
}

var (
	douyinIDRe     = regexp.MustCompile(`/(?:video|note)/(\d{10,25})`)
	douyinModalRe  = regexp.MustCompile(`modal_id=(\d{10,25})`)
	douyinRouterRe = regexp.MustCompile(`(?s)window\._ROUTER_DATA\s*=\s*(\{.*?\})\s*</script>`)
	douyinVideoIDRe = regexp.MustCompile(`video_id=([^&]+)`)
)

type douyinRouterData struct {
	LoaderData map[string]struct {
		VideoInfoRes struct {
			ItemList []struct {
				Desc  string `json:"desc"`
				Video struct {
					Width    int `json:"width"`
					Height   int `json:"height"`
					PlayAddr struct {
						URLList []string `json:"url_list"`
					} `json:"play_addr"`
				} `json:"video"`
			} `json:"item_list"`
		} `json:"videoInfoRes"`
	} `json:"loaderData"`
}

func douyinRatio(w, h int) string {
	minDim := w
	if h > 0 && h < minDim {
		minDim = h
	}
	switch {
	case minDim >= 2160:
		return "4k"
	case minDim >= 1440:
		return "2k"
	case minDim >= 1080:
		return "1080p"
	case minDim >= 720:
		return "720p"
	default:
		return "540p"
	}
}

// resolveDouyin 解析抖音分享页获取直链（原逻辑；请求目标为固定官方域名）。
func resolveDouyin(rawURL, cookie string) (*directVideo, error) {
	awemeID := ""
	if m := douyinIDRe.FindStringSubmatch(rawURL); m != nil {
		awemeID = m[1]
	} else if m := douyinModalRe.FindStringSubmatch(rawURL); m != nil {
		awemeID = m[1]
	}
	if awemeID == "" {
		return nil, fmt.Errorf("无法从链接中提取抖音视频 ID")
	}

	// 固定官方域名请求：不允许用户控制目标（SSRF 安全）
	pageURL := "https://www.iesdouyin.com/share/video/" + awemeID + "/"
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", MobileUA)
	req.Header.Set("Referer", "https://www.douyin.com/")
	if strings.TrimSpace(cookie) != "" {
		req.Header.Set("Cookie", strings.TrimSpace(cookie))
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求抖音分享页失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("抖音分享页返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if err != nil {
		return nil, fmt.Errorf("读取抖音分享页失败: %v", err)
	}

	m := douyinRouterRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("分享页未包含视频数据（_ROUTER_DATA 缺失）")
	}
	var rd douyinRouterData
	if err := json.Unmarshal(m[1], &rd); err != nil {
		return nil, fmt.Errorf("解析分享页数据失败: %v", err)
	}
	for _, page := range rd.LoaderData {
		if len(page.VideoInfoRes.ItemList) == 0 {
			continue
		}
		item := page.VideoInfoRes.ItemList[0]
		if len(item.Video.PlayAddr.URLList) == 0 {
			return nil, fmt.Errorf("分享页未返回播放地址（视频可能已删除或设为私密）")
		}
		vm := douyinVideoIDRe.FindStringSubmatch(item.Video.PlayAddr.URLList[0])
		if vm == nil {
			return nil, fmt.Errorf("未能提取 video_id")
		}
		ratio := douyinRatio(item.Video.Width, item.Video.Height)
		title := strings.TrimSpace(item.Desc)
		if title == "" {
			title = "douyin_" + awemeID
		}
		return &directVideo{
			URL:     fmt.Sprintf("https://aweme.snssdk.com/aweme/v1/play/?line=0&ratio=%s&video_id=%s", ratio, vm[1]),
			Title:   title,
			Width:   item.Video.Width,
			Height:  item.Video.Height,
			Quality: ratio,
		}, nil
	}
	return nil, fmt.Errorf("分享页未返回视频信息（视频可能已删除或设为私密）")
}

// resolveDirectVideo 按平台分发自研解析器。
func resolveDirectVideo(platform, rawURL, cookie string) (*directVideo, error) {
	switch platform {
	case "douyin":
		return resolveDouyin(rawURL, cookie)
	}
	return nil, fmt.Errorf("平台 %s 无自研解析器", platform)
}
