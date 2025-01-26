package main

import (
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/render"
	"github.com/gorilla/websocket"
	"github.com/sstallion/go-hid"
	"gopkg.in/yaml.v3"
)

var sampleRate = 16000

type dialerConfig struct {
	Type     string               `yaml:"type"`    // client, map
	Client   string               `yaml:"client"`  // if type = client
	Format   string               `yaml:"format"`  // if type = client
	Region   string               `yaml:"region"`  // if format = phone
	Numbers  map[string][2]string `yaml:"numbers"` // if type = map
	DialTone []float64            `yaml:"dial-tone"`
}

type configData struct {
	Secret     string                  `yaml:"secret"`
	CygwinPath string                  `yaml:"cygwin-path"` // path of cygwin if running on windows
	CallerID   string                  `yaml:"caller-id"`   // off, before-first-ring, after-first-ring
	Dialers    map[string]dialerConfig `yaml:"dialers"`
	Whitelists map[string][]string     `yaml:"whitelists"`
}

type ringData struct {
	ID         string `json:"id"`
	ClientType string
	CallerID   *calleridData `json:"callerId,omitempty"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	EnableCompression: true,
}

type client interface {
	Call(number string, dialer string)
	End()
	Answer(id string)
	Disconnect() bool
}

var mu sync.Mutex
var clients = map[string]client{}
var hidDevice *hid.Device
var config configData
var offHook = false
var ringing []ringData
var dialTone = false
var clientUsingPhone string

var currentNumbers = ""
var currentDialer = "default"

func ring(cidData *calleridData) {
	if cidData != nil {
		if config.CallerID == "before-first-ring" {
			callerid(*cidData)
		} else if config.CallerID == "after-first-ring" {
			go func() {
				<-time.After(2250 * time.Millisecond)

				mu.Lock()
				if !offHook {
					callerid(*cidData)
				}
				mu.Unlock()
			}()
		}

	}

	hidDevice.SendFeatureReport(startRinging)
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

	hidDevice, err = hid.OpenFirst(1766, 49664)
	if err != nil {
		panic(err)
	}

	hidDevice.SendFeatureReport(stopRinging)

	go hidLoop(hidDevice)

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "DELETE"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
		secret := r.URL.Query().Get("secret")
		if secret != config.Secret {
			render.Status(r, http.StatusUnauthorized)
			render.PlainText(w, r, "invalid secret")
			return
		}

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

		err = callerid(*cidData)
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.PlainText(w, r, err.Error())
		}

		render.NoContent(w, r)
	})

	r.HandleFunc("/ws", handleWebSocketConnection)

	http.ListenAndServe("127.0.0.1:5840", r)
}
