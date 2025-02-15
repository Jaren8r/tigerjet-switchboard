//go:build !windows

package main

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
)

func formatDeviceID(d malgo.DeviceID) string {
	displayLen := len(d)
	for (displayLen > 1) && (d[displayLen-1] == 0) {
		displayLen--
	}

	return string(d[:displayLen])
}

var audioBackends []malgo.Backend

func resolveAudioDeviceID(serial string, deviceType malgo.DeviceType) (audioDeviceId, error) {
	devices, err := audioContext.Devices(deviceType)
	if err != nil {
		return audioDeviceId{}, err
	}

	for _, device := range devices {
		deviceId := formatDeviceID(device.ID)

		if strings.Contains(deviceId, serial) {
			return audioDeviceId{
				malgo: device.ID,
				id:    deviceId,
			}, nil
		}
	}

	return audioDeviceId{}, fmt.Errorf("device not found: %s", serial)
}
