package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/render"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type wsAggregatorClient struct {
	connections []*wsConnection
	typ         string
}

type wsConnection struct {
	id            uuid.UUID
	ws            *websocket.Conn
	client        *wsAggregatorClient
	currentDevice *device
}

func (c *wsAggregatorClient) Call(d *device, data callData, _ string) {
	for _, c := range c.connections {
		if c.currentDevice == nil {
			c.currentDevice = d
			c.ws.WriteJSON([2]any{"call", data})
			break
		}
	}
}

func (c *wsAggregatorClient) End(d *device) {
	for _, c := range c.connections {
		if c.currentDevice == d {
			c.currentDevice = nil
			c.ws.WriteJSON([1]string{"end"})
			break
		}
	}
}

func (c *wsAggregatorClient) Answer(d *device, data callAnswerData) {
	for _, c := range c.connections {
		if c.id == data.ringData.clientId {
			c.currentDevice = d
			c.ws.WriteJSON([2]any{"answer", data})
			break
		}
	}
}

func (c *wsAggregatorClient) InUse() bool {
	for _, c := range c.connections {
		if c.currentDevice == nil {
			return false
		}
	}

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

	var client *wsAggregatorClient

	if existing, ok := clients[clientType]; ok {
		if client, ok = existing.(*wsAggregatorClient); !ok {
			render.Status(r, http.StatusBadRequest)
			render.PlainText(w, r, "unable to register as this client type")
			return
		}
	}

	if client == nil {
		client = &wsAggregatorClient{
			typ: clientType,
		}
		clients[clientType] = client
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	conn := &wsConnection{
		id:     uuid.New(),
		ws:     ws,
		client: client,
	}

	client.connections = append(client.connections, conn)

	mu.Unlock()

	for {
		_, bytes, err := ws.ReadMessage()
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

			ringData.clientType = clientType
			ringData.clientId = conn.id

			mu.Lock()
			ringing.StartRinging(ringData)
			mu.Unlock()
		case "stopRinging":
			var id string
			err = json.Unmarshal(jsonParts[1], &id)
			if err != nil {
				break
			}

			mu.Lock()
			ringing.StopRinging(id)
			mu.Unlock()
		case "dialing":
			var dialing bool
			err = json.Unmarshal(jsonParts[1], &dialing)
			if err != nil {
				break
			}

			mu.Lock()
			if conn.currentDevice != nil {
				if dialing {
					conn.currentDevice.audio.Play(&toneSource{
						frequencies: dialingFrequencies,
						onOff:       dialingOnOff,
					})
				} else {
					conn.currentDevice.audio.Stop()
				}
			}
			mu.Unlock()
		case "end":
			mu.Lock()
			if conn.currentDevice != nil {
				conn.currentDevice.clientUsingPhone = ""
				conn.currentDevice = nil
			}
			mu.Unlock()
		}
	}

	// On Disconnect

	mu.Lock()

	for i, c := range client.connections {
		if c == conn {
			client.connections = append(client.connections[:i], client.connections[i+1:]...)

			if len(client.connections) == 0 {
				delete(clients, clientType)
			}
			break
		}
	}

	mu.Unlock()

	if conn.currentDevice != nil {
		conn.currentDevice.clientUsingPhone = ""
	}

	ws.Close()
}
