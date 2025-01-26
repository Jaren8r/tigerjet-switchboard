package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/wav"
)

var audioMu sync.Mutex

type toneStreamer struct {
	sampleRate  float64
	frequencies []float64
	offset      int
	onOff       [2]int
}

func (s *toneStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	totalOnOff := s.onOff[0] + s.onOff[1]

	for i := range samples {
		point := float64(0)

		if totalOnOff == 0 {
			for _, freq := range s.frequencies {
				point += math.Sin(float64(i+s.offset)*(freq/s.sampleRate)*math.Pi*2) * 0.2
			}
		} else {
			onOffPoint := (i + s.offset) % totalOnOff

			if onOffPoint <= s.onOff[0] {
				for _, freq := range s.frequencies {
					point += math.Sin(float64(i+s.offset)*(freq/s.sampleRate)*math.Pi*2) * 0.2
				}
			}
		}

		samples[i] = [2]float64{point, point}
	}

	s.offset += len(samples)

	return len(samples), true
}

func (s *toneStreamer) Err() error {
	return nil
}

type callerIdStreamer struct {
	stage         uint8
	dir           string
	payloadStream beep.StreamSeekCloser
	ch            <-chan beep.StreamSeekCloser
}

func newCallerIdStreamer(data calleridData) (*callerIdStreamer, error) {
	dir, err := os.MkdirTemp("", "callerid")
	if err != nil {
		return nil, err
	}

	ch := make(chan beep.StreamSeekCloser)

	cidStreamer := &callerIdStreamer{
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

		outputFile, err := os.Open(path.Join(dir, "output.wav"))
		if err != nil {
			panic(err)
		}

		streamer, _, err := wav.Decode(outputFile)
		if err != nil {
			panic(err)
		}

		ch <- streamer
		close(ch)
	}()

	return cidStreamer, nil
}

func (s *callerIdStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	filled := 0
loop:
	for filled < len(samples) {
		switch s.stage {
		case 0:
			n, ok := seizureSteamer.Stream(samples[filled:])
			if !ok {
				seizureSteamer.Seek(0)

				s.stage += 1
			}

			filled += n
		case 1, 3:
			n, ok := carrierSteamer.Stream(samples[filled:])
			if !ok {
				carrierSteamer.Seek(0)

				s.stage += 1
			}

			filled += n
		case 2:
			if s.payloadStream == nil {
				s.payloadStream = <-s.ch
			}

			n, ok := s.payloadStream.Stream(samples[filled:])
			if !ok {
				s.payloadStream.Close()
				os.RemoveAll(s.dir)
				s.stage += 1
			}

			filled += n
		default:
			if filled == 0 {
				return 0, false
			}

			break loop
		}

	}
	return filled, true
}

func (s *callerIdStreamer) Err() error {
	return nil
}

var seizureSteamer beep.StreamSeekCloser
var carrierSteamer beep.StreamSeekCloser

type soundClient struct{}

func (c soundClient) Call(file string, _ string) {
	ext := file[strings.LastIndex(file, ".")+1:]

	f, err := os.Open(file)
	if err != nil {
		fmt.Println(err)
		return
	}

	var fileStreamer beep.StreamSeekCloser
	var format beep.Format

	switch ext {
	case "mp3":
		fileStreamer, format, err = mp3.Decode(f)
	case "wav":
		fileStreamer, format, err = wav.Decode(f)
	default:
		fmt.Printf("unknown file extension %s\n", ext)
		return
	}

	if err != nil {
		fmt.Println(err)
		return
	}

	var streamer beep.Streamer

	if format.SampleRate != beep.SampleRate(sampleRate) {
		streamer = beep.Resample(3, format.SampleRate, beep.SampleRate(sampleRate), fileStreamer)
	} else {
		streamer = fileStreamer
	}

	go func() {
		speaker.PlayAndWait(streamer)
		fileStreamer.Close()
	}()
}

func (c soundClient) End() {
	speaker.Clear()
}

func (c soundClient) Answer(id string) {

}

func (c soundClient) Disconnect() bool {
	return false
}

func init() {
	clients["sound"] = soundClient{}

	seizureFile, err := os.Open("seizure.wav")
	if err != nil {
		panic(err)
	}

	seizureSteamer, _, err = wav.Decode(seizureFile)
	if err != nil {
		panic(err)
	}

	carrierFile, err := os.Open("carrier.wav")
	if err != nil {
		panic(err)
	}

	carrierSteamer, _, err = wav.Decode(carrierFile)
	if err != nil {
		panic(err)
	}

	sr := beep.SampleRate(sampleRate)

	err = speaker.Init(sr, sr.N(50*time.Millisecond))
	if err != nil {
		panic(err)
	}
}
