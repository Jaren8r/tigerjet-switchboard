package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/sasha-s/go-deadlock"
	"github.com/youpy/go-wav"
)

var dialingFrequencies = []float64{440, 480}
var dialingOnOff = [2]int{sampleRate * 2, sampleRate * 4}
var busyFrequencies = []float64{480, 620}
var busyOnOff = [2]int{sampleRate / 2, sampleRate / 2}

type audioDeviceId struct {
	malgo malgo.DeviceID
	id    string
}

func (a audioDeviceId) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.id)
}

func resolveAudioDeviceIDs(serial string) (audioDeviceIds, error) {
	input, err := resolveAudioDeviceID(serial, malgo.Capture)
	if err != nil {
		return audioDeviceIds{}, err
	}

	output, err := resolveAudioDeviceID(serial, malgo.Playback)
	if err != nil {
		return audioDeviceIds{}, err
	}

	return audioDeviceIds{
		Serial: serial,
		Input:  input,
		Output: output,
	}, nil
}

type audioDeviceIds struct {
	Serial string        `json:"serial"`
	Input  audioDeviceId `json:"input"`
	Output audioDeviceId `json:"output"`
}

type audioDevice struct {
	mu           deadlock.Mutex
	device       *malgo.Device
	source       audioSource
	doneCallback func()
}

func (d *audioDevice) stop() {
	if d.doneCallback != nil {
		d.doneCallback()
		d.doneCallback = nil
	}

	d.source = nil
}

func (d *audioDevice) Stop() {
	d.mu.Lock()
	d.stop()
	d.mu.Unlock()
}

func (d *audioDevice) Play(s audioSource) {
	d.mu.Lock()

	d.stop()
	d.source = s

	d.mu.Unlock()
}

func (d *audioDevice) PlayAndWait(s audioSource) {
	ch := make(chan struct{})

	d.mu.Lock()

	d.stop()
	d.source = s
	d.doneCallback = func() {
		ch <- struct{}{}
		close(ch)
	}

	d.mu.Unlock()

	<-ch
}

func (d *audioDevice) PlayCallerID(data calleridData) error {
	if data.Time.IsZero() {
		data.Time = time.Now()
	}

	streamer, err := newCallerIdSource(data)
	if err != nil {
		return err
	}

	d.PlayAndWait(streamer)

	return nil
}

type audioSource interface {
	Read(bytes []byte) (done bool)
}

type toneSource struct {
	frequencies []float64
	offset      int
	onOff       [2]int
}

func (s *toneSource) Read(bytes []byte) (done bool) {
	numSamples := len(bytes) / 2

	samples := unsafe.Slice((*int16)(unsafe.Pointer(&bytes[0])), numSamples)

	totalOnOff := s.onOff[0] + s.onOff[1]

	for i := range samples {
		point := float64(0)

		if totalOnOff == 0 {
			for _, freq := range s.frequencies {
				point += math.Sin(float64(i+s.offset)*(freq/sampleRate)*math.Pi*2) * 0.2
			}

			samples[i] = int16(math.Round(point * 32767))
		} else {
			onOffPoint := (i + s.offset) % totalOnOff

			if onOffPoint <= s.onOff[0] {
				for _, freq := range s.frequencies {
					point += math.Sin(float64(i+s.offset)*(freq/sampleRate)*math.Pi*2) * 0.2
				}

				samples[i] = int16(math.Round(point * 32767))
			}
		}
	}

	s.offset += numSamples

	return false
}

type callerIdSource struct {
	stage         uint8
	dir           string
	offset        int
	payloadFile   *os.File
	payloadStream *wav.Reader
	ch            <-chan *wav.Reader
}

func newCallerIdSource(data calleridData) (*callerIdSource, error) {
	dir, err := os.MkdirTemp("", "callerid")
	if err != nil {
		return nil, err
	}

	ch := make(chan *wav.Reader)

	cidSource := &callerIdSource{
		dir: dir,
		ch:  ch,
	}

	go func() {
		minimodem := "minimodem"
		if runtime.GOOS == "windows" {
			if config.CygwinPath != "" {
				minimodem = path.Join(config.CygwinPath, "usr", "local", "bin", "minimodem.exe")
			} else {
				minimodem += ".exe"
			}
		}

		cmd := exec.Command(minimodem, "--tx", "1200", "-f", "output.wav", "-R", strconv.Itoa(sampleRate))
		cmd.Dir = dir

		if runtime.GOOS == "windows" && config.CygwinPath != "" {
			cmd.Env = os.Environ()
			for i := range cmd.Env {
				if strings.HasPrefix(strings.ToUpper(cmd.Env[i]), "PATH=") {
					cmd.Env[i] = cmd.Env[i] + fmt.Sprintf(";%s\\bin;%s\\usr\\local\\bin", config.CygwinPath, config.CygwinPath)
					break
				}
			}
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			panic(err)
		}

		err = cmd.Start()
		if err != nil {
			panic(err)
		}

		stdin.Write(calleridDataToBytes(data))
		stdin.Close()

		err = cmd.Wait()
		if err != nil {
			panic(err)
		}

		cidSource.payloadFile, err = os.Open(path.Join(dir, "output.wav"))
		if err != nil {
			panic(err)
		}

		ch <- wav.NewReader(cidSource.payloadFile)
		close(ch)
	}()

	return cidSource, nil
}

func (s *callerIdSource) Read(bytes []byte) (done bool) {
	filled := 0

	for filled < len(bytes) {
		switch s.stage {
		case 0:
			n := copy(bytes[filled:], seizureData[s.offset:])
			filled += n
			s.offset += n
			if len(seizureData[s.offset:]) == 0 {
				s.offset = 0
				s.stage += 1
			}
		case 1, 3:
			n := copy(bytes[filled:], carrierData[s.offset:])
			filled += n
			s.offset += n
			if len(carrierData[s.offset:]) == 0 {
				s.offset = 0
				s.stage += 1
			}
		case 2:
			if s.payloadStream == nil {
				s.payloadStream = <-s.ch
			}

			n, err := io.ReadFull(s.payloadStream, bytes)

			filled += n

			if err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) {
					s.stage += 1
				} else {
					panic(err)
				}
			}

		default:
			return true
		}
	}

	return false
}

func (d *audioDevice) deviceCallback(pOutputSample, pInputSamples []byte, framecount uint32) {
	d.mu.Lock()
	if d.source != nil {
		done := d.source.Read(pOutputSample)
		if done {
			d.stop()
		}
	}
	d.mu.Unlock()
}

func newAudioDevice(deviceID malgo.DeviceID) (*audioDevice, error) {
	s := new(audioDevice)

	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.DeviceID = deviceID.Pointer()
	config.Playback.Channels = 1
	config.Playback.Format = malgo.FormatS16
	config.SampleRate = uint32(sampleRate)

	callbacks := malgo.DeviceCallbacks{
		Data: s.deviceCallback,
	}

	d, err := malgo.InitDevice(audioContext.Context, config, callbacks)
	if err != nil {
		return nil, err
	}

	err = d.Start()
	if err != nil {
		return nil, err
	}

	s.device = d

	return s, nil
}

var audioContext *malgo.AllocatedContext
var audioBackend malgo.Backend

func loadWav(name string) []byte {
	seizureFile, err := os.Open(name)
	if err != nil {
		panic(err)
	}

	defer seizureFile.Close()

	data, err := io.ReadAll(wav.NewReader(seizureFile))
	if err != nil {
		panic(err)
	}

	return data
}

var seizureData = loadWav("seizure.wav")
var carrierData = loadWav("carrier.wav")

func init() {
	var err error

	if audioBackends != nil {
		for _, backend := range audioBackends {
			audioContext, err = malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
			if err != nil {
				if errors.Is(err, malgo.ErrNoBackend) {
					continue
				}

				panic(err)
			}

			audioBackend = backend
			break
		}
	} else {
		audioContext, err = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
		if err != nil {
			panic(err)
		}
	}

}
