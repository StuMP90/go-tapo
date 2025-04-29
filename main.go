package main

import (
	"flag"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/beevik/etree"
	onvif "github.com/use-go/onvif"
	"github.com/use-go/onvif/media"
	discover "github.com/use-go/onvif/ws-discovery"
	onvifx "github.com/use-go/onvif/xsd/onvif"
)

// discoverCamerasONVIF discovers ONVIF cameras on the network and returns them as []Camera
func discoverCamerasONVIF() []Camera {
	var cameras []Camera
	// Use empty string for interface to probe all
	devices, err := discover.SendProbe("", nil, []string{"dn:NetworkVideoTransmitter"}, map[string]string{"dn": "http://www.onvif.org/ver10/network/wsdl"})
	if err != nil {
		if debugMode { fmt.Println("ONVIF discovery error:", err) }
		return cameras
	}
	for _, j := range devices {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(j); err != nil {
			if debugMode { fmt.Println("ONVIF XML parse error:", err) }
			continue
		}
		endpoints := doc.Root().FindElements("./Body/ProbeMatches/ProbeMatch/XAddrs")
		for _, xaddr := range endpoints {
			parts := strings.Split(xaddr.Text(), " ")
			if len(parts) > 0 {
				host := parts[0]
				host = strings.Split(host, "/")[2] // Extract host:port
				ip := strings.Split(host, ":")[0]
				// Attempt ONVIF Media GetStreamUri
				var rtspPath string = "/stream1"
				var rtspURL string
				// Try ONVIF media if possible
				// Import onvif and media packages at the top:
				// import (
				//   onvif "github.com/use-go/onvif"
				//   "github.com/use-go/onvif/media"
				// )
				//
				// Use default ONVIF port 80
				dev, err := onvif.NewDevice(onvif.DeviceParams{Xaddr: fmt.Sprintf("%s:80", ip)})
				if err == nil {
					profilesResp, err := dev.CallMethod(media.GetProfiles{})
					if err == nil {
						profilesBody, _ := ioutil.ReadAll(profilesResp.Body)
						profileToken := extractProfileToken(string(profilesBody))
						if profileToken != "" {
							streamResp, err := dev.CallMethod(media.GetStreamUri{
								StreamSetup: onvifx.StreamSetup{
									Stream:    "RTP-Unicast",
									Transport: onvifx.Transport{Protocol: "RTSP"},
								},
								ProfileToken: onvifx.ReferenceToken(profileToken),
							})
							if err == nil {
								streamBody, _ := ioutil.ReadAll(streamResp.Body)
								uri := extractRTSPUri(string(streamBody))
								if uri != "" {
									rtspURL = uri
									if u, err := url.Parse(uri); err == nil {
										rtspPath = u.Path
									}
								}
							}
						}
					}
				}
				if rtspURL == "" {
					rtspURL = fmt.Sprintf("rtsp://user:pass@%s:554%s", ip, rtspPath)
				}
				cam := Camera{
					Name:     "ONVIF Camera",
					IP:       ip,
					RTSPPath: rtspPath,
					RTSPUrl:  rtspURL,
				}
				cameras = append(cameras, cam)
			}
		}
	}
	return cameras
}

// extractProfileToken is a helper to extract the first profile token from the GetProfiles response XML
func extractProfileToken(xml string) string {
	start := strings.Index(xml, "token=\"")
	if start == -1 {
		return ""
	}
	start += len("token=\"")
	end := strings.Index(xml[start:], "\"")
	if end == -1 {
		return ""
	}
	return xml[start : start+end]
}

// extractRTSPUri is a helper to extract the RTSP URI from the GetStreamUri response XML
func extractRTSPUri(xml string) string {
	start := strings.Index(xml, "rtsp://")
	if start == -1 {
		return ""
	}
	end := strings.IndexAny(xml[start:], "<\" ")
	if end == -1 {
		return xml[start:]
	}
	return xml[start : start+end]
}

// ffmpegStreamToImage runs ffmpeg to decode RTSP to JPEG images and sends them to imgChan
func ffmpegStreamToImage(ctx context.Context, rtspURL string, imgChan chan image.Image, wg *sync.WaitGroup) {
	defer func() {
		if debugMode { fmt.Println("[DEBUG] ffmpegStreamToImage exiting for", rtspURL) }
		wg.Done()
	}()
	cmd := exec.Command("ffmpeg", "-rtsp_transport", "tcp", "-i", rtspURL, "-f", "image2pipe", "-vcodec", "mjpeg", "-")
	// Start FFmpeg in its own process group
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if debugMode { fmt.Println("FFmpeg stdout pipe error:", err) }
		return
	}
	if err := cmd.Start(); err != nil {
		if debugMode { fmt.Println("FFmpeg start error:", err) }
		return
	}
	// Robust: kill ffmpeg process group on context cancel
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if debugMode { fmt.Println("[DEBUG] Killing ffmpeg process group for", rtspURL) }
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
			_ = stdout.Close()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		if debugMode { fmt.Println("[DEBUG] Final kill for ffmpeg process group", rtspURL) }
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
		_ = stdout.Close()
	}()
	imgBuf := bytes.NewBuffer(nil)
	for {
		select {
		case <-ctx.Done():
			if debugMode { fmt.Println("[DEBUG] Context cancelled in main ffmpeg loop for", rtspURL) }
			return
		default:
		}
		imgBuf.Reset()
		// Read JPEG SOI
		for {
			select {
			case <-ctx.Done():
				if debugMode { fmt.Println("[DEBUG] Context cancelled in JPEG read loop for", rtspURL) }
				return
			default:
			}
			b := make([]byte, 1)
			_, err := stdout.Read(b)
			if err != nil {
				// If context is done, exit quietly
				if ctx.Err() != nil {
					if debugMode { fmt.Println("[DEBUG] Context done detected after read error for", rtspURL) }
					return
				}
				if debugMode { fmt.Println("FFmpeg read error:", err) }
				return
			}
			imgBuf.WriteByte(b[0])
			if len(imgBuf.Bytes()) > 2 && imgBuf.Bytes()[len(imgBuf.Bytes())-2] == 0xFF && imgBuf.Bytes()[len(imgBuf.Bytes())-1] == 0xD9 {
				break // JPEG EOI
			}
		}
		img, err := jpeg.Decode(bytes.NewReader(imgBuf.Bytes()))
		if err != nil {
			if debugMode { fmt.Println("JPEG decode error:", err) }
			continue
		}
		imgChan <- img
	}
}

// Scan the local subnet for open RTSP (port 554) cameras
func discoverCameras() []Camera {
	var cameras []Camera
	var wg sync.WaitGroup
	var mu sync.Mutex
	maxCams := 4
	// Use your subnet: 192.168.0.1-254
	base := "192.168.0."
	timeout := 300 * time.Millisecond

	for i := 1; i <= 254; i++ {
		ip := fmt.Sprintf("%s%d", base, i)
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:554", ip), timeout)
			if err == nil {
				conn.Close()
				mu.Lock()
				if len(cameras) < maxCams {
					cam := Camera{
						Name:    "RTSP Camera",
						IP:      ip,
						RTSPUrl: fmt.Sprintf("rtsp://user:pass@%s:554/stream1", ip), // TODO: prompt for user/pass
					}
					cameras = append(cameras, cam)
				}
				mu.Unlock()
			}
		}(ip)
		// Stop launching more goroutines if enough cameras found
		mu.Lock()
		if len(cameras) >= maxCams {
			mu.Unlock()
			break
		}
		mu.Unlock()
	}
	wg.Wait()
	return cameras
}

// Helper to create a colored RGBA image of given size
func coloredImage(c color.Color, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// Global debug flag
var debugMode bool

func main() {
	// Add command-line flags
	debug := flag.String("debug", "No", "Enable debug output (Yes/No)")
	flag.Parse()
	
	// Set debug mode based on flag
	debugMode = *debug == "Yes"
	
	// Get TOML file from command-line args
	tomlFile := "tapo-ip.toml"
	if flag.NArg() > 0 {
		tomlFile = flag.Arg(0)
	}

	myApp := app.New()
	myWindow := myApp.NewWindow("Tapo Cameras 2x2")
	myWindow.Resize(fyne.NewSize(800, 600))

	var cameras []Camera
	cams := readTapoCameras(tomlFile)
	if cams != nil {
		cameras = cams
	} else {
		cameras = discoverCamerasONVIF()
		if len(cameras) == 0 {
			cameras = discoverCameras()
		}
		// Write discovered camera IPs to the specified TOML file (as TOML blocks)
		f, err := os.Create(tomlFile)
		if err == nil {
			fmt.Fprintf(f, "# List of discovered Tapo camera configurations\n")
			for _, cam := range cameras {
				fmt.Fprintf(f, "\n[[tapo_cameras]]\n")
				fmt.Fprintf(f, "ip = \"%s\"\n", cam.IP)
				fmt.Fprintf(f, "username = \"%s\"\n", cam.Username)
				fmt.Fprintf(f, "password = \"%s\"\n", cam.Password)
				if cam.RTSPPath != "" {
					fmt.Fprintf(f, "rtsp_path = \"%s\"\n", cam.RTSPPath)
					fmt.Fprintf(f, "lq_rtsp_path = \"%s\"\n", cam.RTSPPath)
				} else {
					fmt.Fprintf(f, "rtsp_path = \"/stream1\"\n")
					fmt.Fprintf(f, "lq_rtsp_path = \"/stream1\"\n")
				}
			}
			f.Close()
		} else {
			if debugMode { fmt.Println("Failed to write", tomlFile+":", err) }
		}
	}

	imgs := make([]*canvas.Image, 4)
	imgChans := make([]chan image.Image, 4)
	wg := sync.WaitGroup{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stream state: HQ/LQ per camera
	streamQualities := make([]bool, 4) // true = HQ, false = LQ
	streamCancelFuncs := make([]context.CancelFunc, 4)

	for i := 0; i < len(cameras) && i < 4; i++ {
		imgChans[i] = make(chan image.Image, 1)
		imgs[i] = canvas.NewImageFromImage(coloredImage(color.RGBA{128, 128, 128, 255}, 640, 360))
		imgs[i].FillMode = canvas.ImageFillContain

		// Start with LQ stream
		streamQualities[i] = false
		camCtx, camCancel := context.WithCancel(ctx)
		streamCancelFuncs[i] = camCancel
		wg.Add(1)
		go ffmpegStreamToImage(camCtx, fmt.Sprintf("rtsp://%s:%s@%s:554%s", cameras[i].Username, cameras[i].Password, cameras[i].IP, cameras[i].LQRTSPPath), imgChans[i], &wg)
	}

	// Use Fyne's animation API for main-thread UI updates
	for i := 0; i < 4; i++ {
		idx := i
		var anim *fyne.Animation
		anim = fyne.NewAnimation(33*time.Millisecond, func(f float32) {
			select {
			case imgData := <-imgChans[idx]:
				imgs[idx].Image = imgData
				imgs[idx].Refresh()
			default:
			}
			// Restart the animation for continuous updates
			anim.Start()
		})
		anim.Curve = fyne.AnimationLinear
		anim.Start()
	}

	// Add HQ/LQ toggle buttons
	qualityButtons := make([]fyne.CanvasObject, 4)
	for i := 0; i < 4; i++ {
		idx := i
		var btn *widget.Button
		btnLabel := "LQ"
		if streamQualities[idx] {
			btnLabel = "HQ"
		}
		btn = widget.NewButton(btnLabel, func() {
			if streamQualities[idx] {
				// Currently HQ, switch to LQ
				streamQualities[idx] = false
				btn.SetText("LQ")
				btn.Refresh()
				streamCancelFuncs[idx]() // Cancel current HQ stream
				camCtx, camCancel := context.WithCancel(ctx)
				streamCancelFuncs[idx] = camCancel
				wg.Add(1)
				go ffmpegStreamToImage(camCtx, fmt.Sprintf("rtsp://%s:%s@%s:554%s", cameras[idx].Username, cameras[idx].Password, cameras[idx].IP, cameras[idx].LQRTSPPath), imgChans[idx], &wg)
			} else {
				// Currently LQ, switch to HQ
				streamQualities[idx] = true
				btn.SetText("HQ")
				btn.Refresh()
				streamCancelFuncs[idx]() // Cancel current LQ stream
				camCtx, camCancel := context.WithCancel(ctx)
				streamCancelFuncs[idx] = camCancel
				wg.Add(1)
				go ffmpegStreamToImage(camCtx, fmt.Sprintf("rtsp://%s:%s@%s:554%s", cameras[idx].Username, cameras[idx].Password, cameras[idx].IP, cameras[idx].RTSPPath), imgChans[idx], &wg)
			}
		})
		btn.Alignment = widget.ButtonAlignCenter
		qualityButtons[i] = container.NewCenter(btn)
	}

	// Compose each cell: image + button at bottom right
	cells := make([]fyne.CanvasObject, 4)
	for i := 0; i < 4; i++ {
		cells[i] = container.NewBorder(nil, qualityButtons[i], nil, nil, imgs[i])
	}

	grid := container.NewGridWithRows(2,
		container.NewGridWithColumns(2, cells[0], cells[1]),
		container.NewGridWithColumns(2, cells[2], cells[3]),
	)

	myWindow.SetContent(grid)

	myWindow.SetOnClosed(func() {
		if debugMode { fmt.Println("[DEBUG] Window closed, cancelling context") }
		cancel()
	})

	myWindow.ShowAndRun()
	// Failsafe: wait up to 2 seconds for goroutines to exit, then force exit
	exitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(exitDone)
	}()
	select {
	case <-exitDone:
		if debugMode { fmt.Println("[DEBUG] All goroutines exited cleanly.") }
	case <-time.After(2 * time.Second):
		if debugMode { fmt.Println("[DEBUG] Force exiting due to stuck goroutine.") }
		os.Exit(0)
	}
}
