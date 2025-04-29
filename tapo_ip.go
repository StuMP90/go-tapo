package main

import (
	"fmt"
	"io/ioutil"

	"github.com/pelletier/go-toml"
)

type Camera struct {
	Name       string
	IP         string
	Username   string
	Password   string
	RTSPPath   string
	LQRTSPPath string
	RTSPUrl    string
}

type tapoCamerasTOML struct {
	Cameras []struct {
		IP         string `toml:"ip"`
		Username   string `toml:"username"`
		Password   string `toml:"password"`
		RTSPPath   string `toml:"rtsp_path"`
		LQRTSPPath string `toml:"lq_rtsp_path"`
	} `toml:"tapo_cameras"`
}

// readTapoCameras reads the specified TOML file and returns a slice of Camera structs, or nil if not present or empty
func readTapoCameras(filename string) []Camera {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil
	}
	var conf tapoCamerasTOML
	err = toml.Unmarshal(data, &conf)
	if err != nil {
		fmt.Println("TOML parse error:", err)
		return nil
	}
	if len(conf.Cameras) == 0 {
		return nil
	}
	var cams []Camera
	for _, c := range conf.Cameras {
		lqPath := c.LQRTSPPath
		if lqPath == "" {
			lqPath = c.RTSPPath
		}
		cam := Camera{
			Name:       "Tapo Camera",
			IP:         c.IP,
			Username:   c.Username,
			Password:   c.Password,
			RTSPPath:   c.RTSPPath,
			LQRTSPPath: lqPath,
			RTSPUrl:    fmt.Sprintf("rtsp://%s:%s@%s:554%s", c.Username, c.Password, c.IP, c.RTSPPath),
		}
		cams = append(cams, cam)
	}
	return cams
}
