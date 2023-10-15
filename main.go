package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/dustin/go-humanize"
	"github.com/tidwall/gjson"
	goproxy "golang.org/x/net/proxy"
)

// Global options
var debugMode bool
var threads = 10
var socks5 = ""
var socks5User string
var socks5Pass string

const userAgent = `Mozilla/5.0 (Windows NT 6.3; Trident/7.0; rv:11.0) like Gecko`

// States
var counter *DownloadStatus
var wg sync.WaitGroup
var waitingThreads = 0

// Video stores informations about a single video on the platform.
type Video struct {
	url, title string
	qualities  map[string]VideoQuality
}

// VideoQuality stores informations about a single file of a specific video.
type VideoQuality struct {
	quality, url, filename string
	filesize               uint64
	ranges                 bool
}

func main() {
	// Print credits
	fmt.Println()
	fmt.Println("| --- Ph Downloader created by kamy ---")
	fmt.Println("| GitHub: https://github.com/kamalesh-sservi/ph-")
	fmt.Println("| --------------------------------------------")
	fmt.Println()

	// Define flags and parse them
	urlPtr := flag.String("url", "empty", "URL of the video to download")
	qualityPtr := flag.String("quality", "highest", "The quality number (eg. 720) or 'highest'")
	outputPtr := flag.String("output", "default", "Path to where the download should be saved or 'default' for the original filename")
	threadsPtr := flag.Int("threads", 5, "The amount of threads to use to download")
	flag.StringVar(&socks5, "socks5", "", "Specify socks5 proxy address for downloading resources")
	flag.StringVar(&socks5User, "socks5user", "", "Socks5 proxy username for authentication") // Define -socks5user flag
	flag.StringVar(&socks5Pass, "socks5pass", "", "Socks5 proxy password for authentication") // Define -socks5pass flag // Define -socks5pass flag
	flag.BoolVar(&debugMode, "debug", false, "Whether you want to activate debug mode or not")
	flag.Parse()

	// Assign variables to flag values
	url := *urlPtr
	quality := *qualityPtr
	outputPath := *outputPtr
	threads = *threadsPtr
	

	// Check if parameters are set
	if url == "empty" {
		fmt.Println("Please pass a valid url with the -url parameter.")
		return
	}

	// Retrieve video details
	videoDetails, err := GetVideoDetails(url, socks5User, socks5Pass)

	if err != nil {
		fmt.Println("An error occoured while retrieving video details:")
		fmt.Println(err)
		return
	}

	// Process given quality
	var highestQuality int64
	selectedQualityName := quality
	if quality == "highest" {
		for i := range videoDetails.qualities {
			currentQuality, _ := strconv.ParseInt(i, 10, 64)
			if currentQuality > highestQuality {
				highestQuality = currentQuality
			}
		}

		selectedQualityName = strconv.FormatInt(highestQuality, 10)
	}

	// Print video details
	fmt.Println("Title: " + videoDetails.title + "\n")
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 6, 8, 2, ' ', 0)

	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t\n", "Quality", "Filename", "Size", "FastDL", "")
	// fmt.Fprintf(w, "\n%s\t%s\t%s\t%s\t%s\t", "-------", "--------", "----", "------", "")

	for _, quality := range videoDetails.qualities {
		x := " "
		if selectedQualityName == quality.quality {
			x = "←"
		}

		fmt.Fprintf(w, "%sp\t%s\t%s\t%t\t%s\t\n", quality.quality, quality.filename, humanize.Bytes(quality.filesize), quality.ranges, x)
	}

	w.Flush()

	if _, ok := videoDetails.qualities[selectedQualityName]; !ok {
		fmt.Println("Quality " + selectedQualityName + " is not available for this video.")
		return
	}

	selectedQuality := videoDetails.qualities[selectedQualityName]

	fmt.Println("")

	if outputPath == "default" {
		outputPath = selectedQuality.filename
	}

	if selectedQuality.ranges {
		SplitDownloadFile(outputPath, selectedQuality, socks5User, socks5Pass)
	} else {
		DownloadFile(outputPath, selectedQuality.url, socks5User, socks5Pass)
	}

}

// Start HTTP GET Request
// - url
// - proxyAddr
func getResp(url string, username, password string) (*http.Response, error) {
	httpTransport := &http.Transport{}
	client := &http.Client{Transport: httpTransport}

	if len(socks5) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Socks5 proxy address is %s\n", socks5)
		dialer, err := goproxy.SOCKS5("tcp", socks5, &goproxy.Auth{User: username, Password: password}, goproxy.Direct)
		if err != nil {
			return nil, err
		}

		httpTransport.DialContext = func(ctx context.Context, network, addr string) (conn net.Conn, e error) {
			return dialer.Dial(network, addr)
		}
	}

	request, err := http.NewRequest("GET", url, nil)
	request.Header.Add("User-Agent", userAgent)
	request.Header.Add("Referer", url)
	if err != nil {
		return nil, err
	}

	return client.Do(request)
}

// GetVideoDetails queries the given URL and returns details such as
// - title
// - available qualities
func GetVideoDetails(url string, username, password string) (Video, error) {
	slashRegexRule := "\\\\/"
	titleRegexRule := "<title>(.*) - /title>"

	//  regex rules
	slashRegex, _ := regexp.Compile(slashRegexRule)
	titleRegex, _ := regexp.Compile(titleRegexRule)

	// Download content of the webpage
	resp, err := getResp(url, username, password)
	// Check if there was an error downloading
	if err != nil {
		return Video{}, err
	}

	// Get the body and manipulate it to make searching with regex easier
	source, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Video{}, err
	}
	body := slashRegex.ReplaceAllString(string(source), "/")

	// Find and extract JSON-like data
	json := ""
	jsonPattern := `var\s+flashvars_\d+\s*=\s*(.+?});`
	jsonMatches := regexp.MustCompile(jsonPattern).FindAllStringSubmatch(body, -1)

	for _, matches := range jsonMatches {
		if len(matches) >= 2 {
			json = matches[1]
			break
		}
	}

	// Replace some invalid values in JSON (similar to your Java code)
	json = strings.Replace(json, "\\/", "/", -1)

	// Modify the JSON to make it valid JSON
	json = json + "}"

	// Parse the JSON
	flashvarsJSON := gjson.Parse(json)

	// Get values from the JSON
	videoURL := flashvarsJSON.Get("mediaDefinitions.0.videoUrl").String()
	title := titleRegex.FindStringSubmatch(body)[1]
	videoQualities := make(map[string]VideoQuality)

	// Do the Head-Request to the file to fetch some data
	headResp, err := http.Head(videoURL)
	if err != nil {
		fmt.Println("Error while preparing download:")
		fmt.Println(err)
	}
	defer headResp.Body.Close()

	// Get the size of the file
	filesize, _ := strconv.ParseUint(headResp.Header.Get("Content-Length"), 10, 64)

	// Check if the server supports the Range-header
	ranges := headResp.Header.Get("Accept-Ranges") == "bytes"

	// Create and store the video quality object
	videoQuality := VideoQuality{url: videoURL, quality: "unknown", filename: "video.mp4", filesize: filesize, ranges: ranges}
	videoQualities["480"] = videoQuality

	// Check if there are videos on the website. If not, cancel
	if len(videoQualities) == 0 {
		f, _ := os.Create("site.html")
		f.WriteString(body)
		return Video{}, errors.New("could not find any video sources")
	}

	// Sort qualities descending
	var qualityKeys []string
	for k := range videoQualities {
		qualityKeys = append(qualityKeys, k)
	}
	sort.Strings(qualityKeys)

	sortedQualities := make(map[string]VideoQuality)
	for _, k := range qualityKeys {
		sortedQualities[k] = videoQualities[k]
	}

	// Return the video detail instance
	video := Video{url: url, title: title, qualities: sortedQualities}
	return video, nil
}

// DownloadStatus counts the written bytes. Because it implements the io.Writer interface,
// it can be given to the io.TeeReader(). This is also used to print out the current
// status of the download
type DownloadStatus struct {
	Done, Total uint64
}

// Write implements io.Write
func (status *DownloadStatus) Write(bytes []byte) (int, error) {
	// Count the amount of bytes written since the last cycle
	byteAmount := len(bytes)

	// Increment current count by the amount of bytes written since the last cycle
	status.Done += uint64(byteAmount)

	// Update progress
	status.PrintDownloadStatus()

	// Return byteAmount
	return byteAmount, nil
}

// SplitDownloadFile downloads a remote file to the harddrive while writing it
// directly to a file instead of storing it in RAM until the donwload completes.
func SplitDownloadFile(filepath string, video VideoQuality, username, password string) error {
	counter = &DownloadStatus{Total: video.filesize}
	sliceSize := video.filesize / uint64(threads)

	for i := 1; i <= threads; i++ {
		offset := sliceSize * uint64(i-1)
		end := offset + sliceSize - 1

		if i == threads {
			end = video.filesize
		}

		// Create a temporary file
		tempfileName := fmt.Sprintf("%s.%d.tmp", filepath, i)
		output, err := os.Create(tempfileName)
		if err != nil {
			return err
		}

		wg.Add(1)
		go DoPartialDownload(video.url, offset, end, output, socks5User, socks5Pass)
	}

	fmt.Printf("Downloading file using %d threads.\n", threads)
	wg.Wait()
	counter.PrintDownloadStatus()
	fmt.Print("\nProcessing... ")

	// Combine single downloads into a single video file
	output, _ := os.Create(filepath)
	defer output.Close()
	for i := 1; i <= threads; i++ {
		// Open temporary file
		tempfileName := fmt.Sprintf("%s.%d.tmp", filepath, i)
		file, _ := os.Open(tempfileName)

		// Read bytes
		stat, _ := file.Stat()
		tempBytes := make([]byte, stat.Size())
		file.Read(tempBytes)

		// Write to output file
		output.Write(tempBytes)

		// Close and delete temporary file
		file.Close()

		err := os.Remove(tempfileName)
		if err != nil {
			fmt.Println(err)
		}
	}

	fmt.Println("Done!")
	fmt.Println()

	return nil
}

// DoPartialDownload downloads a special part of the file at the given URL.
func DoPartialDownload(url string, offset uint64, end uint64, output *os.File, username, password string) ([]byte, error) {
	defer wg.Done()
	client := http.Client{}

	// Build request
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	req.Header.Add("User-Agent", userAgent)
	req.Header.Add("Referer", url)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Create our progress reporter and pass it to be used alongside our writer
	_, err = io.Copy(output, io.TeeReader(bytes.NewReader(data), counter))
	if err != nil {
		return nil, err
	}

	output.Close()
	waitingThreads++

	return data, nil
}

// DownloadFile downloads a remote file to the harddrive while writing it
// directly to a file instead of storing it in RAM until the donwload completes.
// DownloadFile downloads a remote file to the hard drive while writing it
// directly to a file instead of storing it in RAM until the download completes.
func DownloadFile(filepath string, url string, username, password string) error {
	fmt.Println("Server does not support partial downloads. Continuing with a single thread.")

	// Create a temporary file
	tempfile := filepath + ".tmp"
	output, err := os.Create(tempfile)
	if err != nil {
		return err
	}
	defer output.Close()

	// Download data from the given URL
	// resp, err := http.Get(url)
	resp, err := getResp(url, username, password)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read total size of the file
	filesize, _ := strconv.ParseUint(resp.Header.Get("Content-Length"), 10, 64)
	counter := &DownloadStatus{Total: filesize}

	// Create our progress reporter and pass it to be used alongside our writer
	_, err = io.Copy(output, io.TeeReader(resp.Body, counter)) // Use the global counter here
	if err != nil {
		return err
	}

	// Rename the temp file to the correct ending
	err = os.Rename(tempfile, filepath)
	if err != nil {
		return err
	}

	return nil
}

// PrintDownloadStatus prints the current download progress to console.
// PrintDownloadStatus prints the current download progress to console.
func (status DownloadStatus) PrintDownloadStatus() {
	// Print current status
	fmt.Printf("\rDownloading %s / %s (%d of %d threads done) ", humanize.Bytes(status.Done), humanize.Bytes(status.Total), waitingThreads, threads)
}
