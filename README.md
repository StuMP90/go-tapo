# go-tapo: Tapo Camera 2x2 GUI

This Go program discovers up to 4 Tapo cameras on your local network and displays their RTSP streams in a 2x2 grid GUI.

## Requirements
- Go 1.18+
- Tapo cameras with RTSP enabled (set up in Tapo app)
- (Optional) RTSP username/password for each camera

## Usage
1. Build and run the program:
   ```bash
   go run main.go
   ```
2. The program will try to auto-discover cameras. If discovery fails, you can manually enter camera IPs in `tapo-ip.toml`.

## Notes
- The program uses Fyne for the GUI and [gortsplib v1](https://github.com/aler9/gortsplib/tree/v1.0.1) for RTSP streaming.
- By default, it connects to the RTSP stream at `rtsp://<ip>:554/stream1` with no credentials. If your camera requires credentials, update the RTSP URL in the code or `tapo-ip.toml`.
- If you encounter issues with video playback, ensure your cameras have RTSP enabled, your firewall allows streaming, and the RTSP URL is correct.
- If you see a black window or no stream, your camera may not support unauthenticated RTSP or may use a different stream path.
