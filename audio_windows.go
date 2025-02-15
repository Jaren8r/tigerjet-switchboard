package main

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var audioBackends = []malgo.Backend{
	malgo.BackendWasapi,
	malgo.BackendDsound,
}

func getParentIdPrefixes() (map[string]string, error) {
	m := make(map[string]string)

	path := fmt.Sprintf(`SYSTEM\CurrentControlSet\Enum\USB\VID_%04X&PID_%04X`, vendorId, productId)
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.RESOURCE_LIST)
	if err != nil {
		return nil, err
	}

	defer k.Close()

	keys, err := k.ReadSubKeyNames(0)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, fmt.Sprintf(`%s\%s`, path, key), registry.READ)
		if err != nil {
			return nil, err
		}

		parentId, _, err := k.GetStringValue("ParentIdPrefix")
		if err != nil {
			return nil, err
		}

		m[key] = strings.ToUpper(parentId)
	}

	return m, nil
}

func resolveWindowsAudioDevices(parentIdPrefixes map[string]string, typ string) (map[string]string, error) {
	m := make(map[string]string)

	path := `SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\` + typ
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.RESOURCE_LIST)
	if err != nil {
		return nil, err
	}

	defer k.Close()

	keys, err := k.ReadSubKeyNames(0)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, fmt.Sprintf(`%s\%s\Properties`, path, key), registry.READ)
		if err != nil {
			return nil, err
		}

		s, _, err := k.GetStringValue("{b3f8fa53-0004-438e-9003-51a46e139bfc},2")
		if err != nil {
			return nil, err
		}

		for serial, prefix := range parentIdPrefixes {
			if strings.Contains(s, prefix) {
				m[serial] = key
			}
		}
	}

	return m, nil
}

var windowsInputDevices map[string]string
var windowsOutputDevices map[string]string

func init() {
	parentIds, err := getParentIdPrefixes()
	if err != nil {
		panic(err)
	}

	windowsInputDevices, err = resolveWindowsAudioDevices(parentIds, "Capture")
	if err != nil {
		panic(err)
	}

	windowsOutputDevices, err = resolveWindowsAudioDevices(parentIds, "Render")
	if err != nil {
		panic(err)
	}
}

func resolveAudioDeviceID(serial string, deviceType malgo.DeviceType) (audioDeviceId, error) {
	var deviceId string
	switch deviceType {
	case malgo.Capture:
		deviceId = "{0.0.1.00000000}." + windowsInputDevices[serial]
	case malgo.Playback:
		deviceId = "{0.0.0.00000000}." + windowsOutputDevices[serial]
	}

	devices, err := audioContext.Devices(deviceType)
	if err != nil {
		return audioDeviceId{}, err
	}

	switch audioBackend {
	case malgo.BackendWasapi:
		for _, device := range devices {
			if windows.UTF16PtrToString((*uint16)(device.ID.Pointer())) == deviceId {
				return audioDeviceId{
					malgo: device.ID,
					id:    deviceId,
				}, nil
			}
		}
	case malgo.BackendDsound:
		suffix := strings.Replace(deviceId[37:54], "-", "", 1)
		for _, device := range devices {
			if strings.HasSuffix(device.ID.String(), suffix) {
				return audioDeviceId{
					malgo: device.ID,
					id:    deviceId,
				}, nil
			}
		}
	default:
		panic("invalid audio backend")
	}

	return audioDeviceId{}, fmt.Errorf("device not found: %s", serial)
}
