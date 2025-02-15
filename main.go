package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/render"
	"github.com/gorilla/websocket"
	"github.com/sasha-s/go-deadlock"
	"github.com/sstallion/go-hid"
	"gopkg.in/yaml.v3"
)

const vendorId = 1766
const productId = 49664
const sampleRate = 16000

type ringListItem [2]string

func (r *ringListItem) UnmarshalYAML(unmarshal func(any) error) error {
	err := unmarshal((*[2]string)(r))
	if err != nil {
		err := unmarshal(&r[0])
		if err != nil {
			return err
		}
	}

	return nil
}

type dialerConfig struct {
	Client             string               `yaml:"client"`               // if type = client
	ClientNumberFormat string               `yaml:"client-number-format"` // if type = client
	ClientNumberRegion string               `yaml:"client-number-region"` // if format = phone
	Map                map[string][2]string `yaml:"map"`                  // if type = map
	DialTone           []float64            `yaml:"dial-tone"`
}

type deviceConfig struct {
	Dialer       string         `yaml:"dialer"`
	CallerID     string         `yaml:"caller-id"` // off, before-first-ring, after-first-ring
	RingListType string         `yaml:"ring-list-type"`
	RingList     []ringListItem `yaml:"ring-list"`
}

type configData struct {
	Secret     string                  `yaml:"secret"`
	CygwinPath string                  `yaml:"cygwin-path"` // path of cygwin if running on windows
	Dialers    map[string]dialerConfig `yaml:"dialers"`
	Devices    map[string]deviceConfig `yaml:"devices"`
}

type callData struct {
	Number string         `json:"number"`
	Device audioDeviceIds `json:"device"`
}

type callAnswerData struct {
	ID       string         `json:"id"`
	Device   audioDeviceIds `json:"device"`
	ringData ringData
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type client interface {
	Call(d *device, data callData, dialer string)
	End(d *device)
	Answer(d *device, data callAnswerData)
	InUse() bool
}

type dialerClient struct {
}

func (c dialerClient) Call(d *device, data callData, dialer string) {
	d.clientUsingPhone = ""

	newDialer, ok := config.Dialers[data.Number]
	if !ok {
		d.dialer = ""
		d.audio.Play(&toneSource{
			frequencies: busyFrequencies,
			onOff:       busyOnOff,
		})
		return
	}

	d.audio.Play(&toneSource{
		frequencies: newDialer.DialTone,
	})
	d.dialTone = true
	d.dialpad = ""
	d.dialer = data.Number
}

func (c dialerClient) End(_ *device) {
}

func (c dialerClient) Answer(_ *device, data callAnswerData) {
}

func (c dialerClient) Ringing(_ *device) []ringData {
	return nil
}

func (c dialerClient) InUse() bool {
	return false
}

type device struct {
	serial                     string
	silver                     bool // classic mj device
	inUse                      bool
	dialTone                   bool
	ringing                    bool
	clientUsingPhone           string
	dialer                     string
	dialpad                    string
	hid                        *hid.Device
	audio                      *audioDevice
	audioDeviceIds             audioDeviceIds
	stopCallerID               context.CancelFunc
	stopSilverRinger           context.CancelFunc
	sendHidSyncedFeatureReport chan []byte
}

var devices []*device
var clients = map[string]client{}
var mu deadlock.Mutex
var config configData

func (d *device) config() deviceConfig {
	if config, ok := config.Devices[d.serial]; ok {
		return config
	}

	return config.Devices["default"]
}

func (d *device) shouldRing(client string, number string) bool {
	c := d.config()

	switch c.RingListType {
	case "whitelist":
		for _, item := range c.RingList {
			if item[0] == client && (item[1] == "" || item[1] == number) {
				return true
			}
		}

		return false
	case "blacklist":
		for _, item := range c.RingList {
			if item[0] == client && (item[1] == "" || item[1] == number) {
				return false
			}
		}

		return true
	default:
		panic(fmt.Sprintf("invalid ring list type: %s", c.RingListType))
	}
}

func (d *device) ring(cidData *calleridData) {
	if cidData != nil {
		var ctx context.Context
		ctx, d.stopCallerID = context.WithCancel(context.Background())
		config := d.config()

		if config.CallerID == "before-first-ring" {
			go func() {
				d.audio.PlayCallerID(*cidData)
				d.startRinging()
			}()
		} else if config.CallerID == "after-first-ring" {
			go func() {
				select {
				case <-time.After(2250 * time.Millisecond):
					mu.Lock()
					d.stopCallerID = nil
					inUse := d.inUse
					mu.Unlock()

					if !inUse {
						d.audio.PlayCallerID(*cidData)
					}
				case <-ctx.Done():
					// Cancelled
				}
			}()

			d.startRinging()
		}
	}
}

func init() {
	if os.Getenv("DEBUG") == "1" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	clients["dialer"] = dialerClient{}
}

func main() {
	configFile, err := os.ReadFile("config.yml")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			configFile, err = os.ReadFile("config-example.yml")
			if err != nil {
				panic(err)
			}

			err = os.WriteFile("config.yml", configFile, 0644)
			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	}

	err = yaml.Unmarshal(configFile, &config)
	if err != nil {
		panic(err)
	}

	err = hid.Enumerate(vendorId, productId, func(info *hid.DeviceInfo) error {
		h, err := hid.Open(vendorId, productId, info.SerialNbr)
		if err != nil {
			return err
		}

		audioDeviceIds, err := resolveAudioDeviceIDs(info.SerialNbr)
		if err != nil {
			return err
		}

		audio, err := newAudioDevice(audioDeviceIds.Output.malgo)
		if err != nil {
			return err
		}

		d := &device{
			serial:         info.SerialNbr,
			hid:            h,
			audio:          audio,
			audioDeviceIds: audioDeviceIds,
		}

		d.hidInit()

		slog.Info(fmt.Sprintf(
			"[%s] Device connected (Silver=%t,Input=%s,Output=%s)\n",
			d.serial,
			d.silver,
			d.audioDeviceIds.Input.id,
			d.audioDeviceIds.Output.id,
		))

		devices = append(devices, d)

		return nil
	})
	if err != nil {
		panic(err)
	}

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "DELETE"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/debug", func(w http.ResponseWriter, r *http.Request) {
		secret := r.URL.Query().Get("secret")
		if secret != config.Secret {
			render.Status(r, http.StatusUnauthorized)
			render.PlainText(w, r, "invalid secret")
			return
		}
		file, err := os.Open("debug.html")
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.PlainText(w, r, err.Error())
			return
		}

		_, err = io.Copy(w, file)
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.PlainText(w, r, err.Error())
			return
		}

		file.Close()
	})

	r.Post("/callerid", func(w http.ResponseWriter, r *http.Request) {
		secret := r.URL.Query().Get("secret")
		if secret != config.Secret {
			render.Status(r, http.StatusUnauthorized)
			render.PlainText(w, r, "invalid secret")
			return
		}

		cidData := &calleridData{}
		err := render.DecodeJSON(r.Body, cidData)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.PlainText(w, r, err.Error())
			return
		}

		for _, d := range devices {
			go d.audio.PlayCallerID(*cidData)
		}

		render.NoContent(w, r)
	})

	r.HandleFunc("/ws", handleWebSocketConnection)

	http.ListenAndServe("127.0.0.1:5840", r)
}
