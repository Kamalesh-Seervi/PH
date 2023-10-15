package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
	"github.com/robertkrimen/otto"
)

var (
	baseUrl = `https://cn.pornhub.com/view_video.php?viewkey=650297ebec3d8`
)
var (
	ErrWrongUrl            = errors.New("wrong url")
	ErrHttpGetFailed       = errors.New("get url failed")
	ErrParseHtmlPageFailed = errors.New("parse html page failed")
)

type VideoInfo struct {
	ID            string        `json:"id"`            // ID
	Title         string        `json:"title"`         //标题
	Description   string        `json:"description"`   //描述
	DatePublished time.Time     `json:"datePublished"` //发表日期
	Uploader      string        `json:"uploader"`      //上传者
	Duration      time.Duration `json:"duration"`      //时长
	ThumbnailUrl  string
	Files         []*FileInfo
}

type FileInfo struct {
	Number        int    `json:"number"`
	Extension     string `json:"extension"`  //格式
	Resolution    string `json:"resolution"` //解析度
	VideoEncoding string `json:"videoEncoding"`
	AudioEncoding string `json:"audioEncoding"`
	AudioBitrate  int    `json:"audioBitrate"`
	FPS           int    `json:"fps"`  // FPS are frames per second
	Url           string `json:"url"`  //视频下载地址
	Size          int64  `json:"size"` //视频大小
}

func main() {
	fmt.Println("Hello ph")
	router := gin.Default()

	// Route for fetching video information
	router.GET("/video-info", GetVideoInfoByKey)


		video, err := GetVideoInfoByKey(viewKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, video)
	})

	// Route for fetching data from a URL
	router.GET("/fetch-url", func(c *gin.Context) {
		requestedURL := c.Query("url")
		if requestedURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing URL"})
			return
		}

		response, err := httpGet(requestedURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer response.Body.Close()

		// Process the response as needed
		// In this example, we return the status code and headers
		c.JSON(http.StatusOK, gin.H{
			"status":  response.Status,
			"headers": response.Header,
		})
	})

	// Run the Gin server on port 8080
	router.Run(":8080")
}

func GetVideoInfoByUrl(rawUrl string) (video *VideoInfo, err error) {
	u, _ := url.Parse(rawUrl)
	viewkey := u.Query().Get("viewkey")
	if viewkey != "" {
		return GetVideoInfoByKey(viewkey)
	}
	return nil, ErrWrongUrl
}

func GetVideoInfoByKey(c *gin.Context) {
	viewKey := c.Query("viewkey") // Get the "viewkey" query parameter from the request

	if viewKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing viewkey parameter"})
		return
	}

	// The rest of the function remains the same
	url := fmt.Sprintf("%s%s", baseUrl, viewKey)

	resp, err := http.Get(url) // Perform the HTTP GET request
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// Process the response as needed
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch data"})
		return
	}

	// Process the response here and return the data
	video, err := getVideoInfoFromResponse(resp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	video.ID = viewKey
	c.JSON(http.StatusOK, video)
}

func getVideoInfoFromResponse(resp *http.Response) (video *VideoInfo, err error) {
	if resp.StatusCode != http.StatusOK {
		return nil, ErrHttpGetFailed
	}
	flashVar, err := parseHtml(resp)
	if err != nil {
		panic(err)

	}
	video, err = getInfo(flashVar)
	if err != nil {
		panic(err)

	}
	return video, nil
}

func getInfo(obj map[string]interface{}) (video *VideoInfo, err error) {
	defer func() {
		e := recover()
		if e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()
	video = &VideoInfo{}
	video.Title = obj["video_title"].(string)
	video.ThumbnailUrl = obj["image_url"].(string)
	durationStr := obj["video_duration"].(string)
	duration, err := strconv.Atoi(durationStr)
	if err != nil {
		panic(err)

	}
	video.Duration = time.Duration(duration) * time.Second

	mediaDefinitions := obj["mediaDefinitions"].([]map[string]interface{})
	for i, v := range mediaDefinitions {
		resolution, ok := v["quality"].(string)
		if !ok {
			continue
		}

		file := new(FileInfo)
		file.Number = i
		file.Extension = v["format"].(string)
		if file.Extension != "mp4" {
			continue
		}
		file.Resolution = resolution

		file.Url = v["videoUrl"].(string)
		video.Files = append(video.Files, file)
	}
	return video, nil
}

func parseHtml(resp *http.Response) (obj map[string]interface{}, err error) {
	//如何解析pornhub视频地址
	//https://zgao.top/%e7%9c%8b%e5%ae%8cpornhub%e7%9a%84%e8%a7%86%e9%a2%91%e6%8e%a5%e5%8f%a3js%e6%b7%b7%e6%b7%86%e5%90%8e%ef%bc%8c%e6%88%91%e9%a1%ba%e6%89%8b%e5%86%99%e4%ba%86%e4%b8%aa%e4%b8%8b%e8%bd%bd%e6%8f%92%e4%bb%b6/

	html, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		panic(err)

	}
	playerDiv := html.Find("#player")
	id, ok := playerDiv.Attr("data-video-id")
	if !ok {
		return nil, ErrParseHtmlPageFailed
	}

	scriptDiv := playerDiv.Find("script")
	scripts := scriptDiv.Text()
	script := strings.Split(scripts, "loadScriptUniqueId")[0]

	vm := otto.New()
	_, err = vm.Run(script)
	if err != nil {
		panic(err)

	}

	value, err := vm.Get("flashvars_" + id)
	if err != nil {
		panic(err)

	}
	object, err := value.Export()
	if err != nil {
		panic(err)

	}
	objMap, ok := object.(map[string]interface{})
	if !ok {
		return nil, ErrParseHtmlPageFailed
	}
	return objMap, nil
}
