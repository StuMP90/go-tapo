# go-tapo: Tapo Camera 2x2 GUI

This Go program discovers up to 4 Tapo cameras on your local network and displays their RTSP streams in a 2x2 grid GUI. It supports switching between high-quality and low-quality streams for each camera individually.

## Requirements
- Go 1.18+
- Tapo cameras with RTSP enabled (set up in Tapo app)
- FFmpeg installed on your system
- (Optional) RTSP username/password for each camera

## Usage

### Basic Usage
```bash
# Build the application
CGO_ENABLED=1 GOOS=linux go build -o tapo

# Run with default settings
./tapo [toml_file]
```

### Command-Line Options
```bash
# Enable debug output
./tapo -debug=Yes [toml_file]
```

### Configuration
The program will try to auto-discover cameras. If discovery fails, you can manually configure cameras in `tapo-ip.toml`:

```toml
# Example tapo-ip.toml configuration
[[tapo_cameras]]
ip = "192.168.1.100"
username = "admin"
password = "password"
rtsp_path = "/stream1"    # High-quality stream path
lq_rtsp_path = "/stream2" # Low-quality stream path (optional)
```

## Features

### HQ/LQ Video Toggle
Each camera view has a toggle button at the bottom that allows switching between:
- **HQ**: High-quality stream (uses `rtsp_path`)
- **LQ**: Low-quality stream (uses `lq_rtsp_path`)

By default, cameras start in LQ mode to conserve bandwidth. If `lq_rtsp_path` is not specified, it will use the same path as `rtsp_path`.

### Debug Mode
Use the `-debug=Yes` flag to enable detailed debug output, which can be helpful for troubleshooting connection issues.

## Notes
- The program uses Fyne for the GUI and FFmpeg for RTSP stream decoding.
- If you encounter issues with video playback, ensure your cameras have RTSP enabled, your firewall allows streaming, and the RTSP URL is correct.
- If you see a black window or no stream, your camera may not support unauthenticated RTSP or may use a different stream path.
