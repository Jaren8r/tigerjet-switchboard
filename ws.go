package main

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/go-chi/render"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gorilla/websocket"
)

type wsClient struct {
	ws  *websocket.Conn
	typ string
}

func (c wsClient) Call(number string, _ string) {
	c.ws.WriteJSON([2]string{"call", number})
}

func (c wsClient) End() {
	c.ws.WriteJSON([2]string{"end"})
}

func (c wsClient) Answer(id string) {
	c.ws.WriteJSON([2]string{"answer", id})
}

func (c wsClient) Disconnect() bool {
	c.ws.Close()

	return true
}

func handleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	clientType := r.URL.Query().Get("client")
	secret := r.URL.Query().Get("secret")

	if clientType == "" {
		render.Status(r, http.StatusBadRequest)
		render.PlainText(w, r, "missing client type")
		return
	}

	if secret != config.Secret {
		render.Status(r, http.StatusUnauthorized)
		render.PlainText(w, r, "invalid secret")
		return
	}

	mu.Lock()

	if existing, ok := clients[clientType]; ok {
		if !existing.Disconnect() {
			render.Status(r, http.StatusBadRequest)
			render.PlainText(w, r, "unable to register as this client type")
			return
		}
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := wsClient{
		ws:  c,
		typ: clientType,
	}

	clients[clientType] = client

	mu.Unlock()

	for {
		_, bytes, err := c.ReadMessage()
		if err != nil {
			break
		}

		var jsonParts [2]json.RawMessage
		err = json.Unmarshal(bytes, &jsonParts)
		if err != nil {
			break
		}

		var typ string

		err = json.Unmarshal(jsonParts[0], &typ)
		if err != nil {
			break
		}

		switch typ {
		case "ring":
			var ringData ringData
			err = json.Unmarshal(jsonParts[1], &ringData)
			if err != nil {
				break
			}

			ringData.ClientType = client.typ

			if whitelist, ok := config.Whitelists[clientType]; ok {
				if slices.Contains(whitelist, ringData.ID) {
					mu.Lock()
					if len(ringing) == 0 {
						ring(ringData.CallerID)
					}
					ringing = append(ringing, ringData)
					mu.Unlock()
				}
			} else {
				mu.Lock()
				if len(ringing) == 0 && !offHook {
					ring(ringData.CallerID)
				}
				ringing = append(ringing, ringData)
				mu.Unlock()
			}

		case "stopRinging":
			var id string
			err = json.Unmarshal(jsonParts[1], &id)
			if err != nil {
				break
			}

			mu.Lock()
			for i := range ringing {
				if ringing[i].ID == id && ringing[i].ClientType == client.typ {
					ringing = append(ringing[:i], ringing[i+1:]...)

					if len(ringing) == 0 && !offHook {
						hidDevice.SendFeatureReport(stopRinging)
					}
				}
			}
			mu.Unlock()
		case "dialing":
			var dialing bool
			err = json.Unmarshal(jsonParts[1], &dialing)
			if err != nil {
				break
			}

			if clientUsingPhone == client.typ {
				mu.Lock()

				if dialing {
					speaker.Play(&toneStreamer{
						sampleRate:  float64(sampleRate),
						frequencies: []float64{440, 480},
						onOff:       [2]int{sampleRate * 2, sampleRate * 4},
					})
				} else {
					speaker.Clear()
				}

				mu.Unlock()
			}
		}

	}

	// On Disconnect

	mu.Lock()

loop:
	for {
		for i := range ringing {
			if ringing[i].ClientType == client.typ {
				ringing = append(ringing[:i], ringing[i+1:]...)

				if len(ringing) == 0 && !offHook {
					hidDevice.SendFeatureReport(stopRinging)
					break loop
				}

				continue loop
			}
		}

		break
	}

	if clientUsingPhone == clientType {
		clientUsingPhone = ""
	}

	if clients[clientType] == client {
		delete(clients, clientType)
	}

	mu.Unlock()

	c.Close()
}
