package main

import (
	"regexp"
	"strconv"

	"github.com/gopxl/beep/v2/speaker"
	"github.com/nyaruka/phonenumbers"
	"github.com/sstallion/go-hid"
)

var startRinging = []byte{0x0, 0x20, 0x0, 0x0, 0x1, 0x1, 0x03, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}
var stopRinging = []byte{0x0, 0x20, 0x0, 0x0, 0x1, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}

func hidLoop(hidDevice *hid.Device) {
	var bytes [2]byte

	for {
		_, err := hidDevice.Read(bytes[:])
		if err != nil {
			panic(err)
		}

		mu.Lock()

		if bytes[1] == 128 {
			if !offHook {
				offHook = true
				speaker.Clear()
				hidDevice.SendFeatureReport(stopRinging)

				if len(ringing) > 0 {
					if client, ok := clients[ringing[0].ClientType]; ok {
						clientUsingPhone = ringing[0].ClientType
						currentDialer = "invalid"
						client.Answer(ringing[0].ID)
					}
				}

				if clientUsingPhone == "" {
					speaker.Play(&toneStreamer{
						sampleRate:  float64(sampleRate),
						frequencies: config.Dialers[currentDialer].DialTone,
					})

					dialTone = true
				}
			}
		} else if offHook {
			offHook = false
			currentNumbers = ""
			currentDialer = "default"
			speaker.Clear()
			dialTone = false

			if clientUsingPhone != "" {
				clients[clientUsingPhone].End()

				clientUsingPhone = ""
			}

			if len(ringing) > 0 {
				ring(ringing[0].CallerID)
			}
		}

		if bytes[0] > 0 && currentDialer != "invalid" {
			if dialTone {
				speaker.Clear()
				dialTone = false
			}
			switch bytes[0] {
			case 11:
				currentNumbers += "*"
			case 12:
				currentNumbers += "#"
			default:
				currentNumbers += strconv.Itoa(int(bytes[0] - 1))
			}

			if currentDialer == "default" {
				if dialer, ok := config.Dialers[currentNumbers]; ok {
					currentDialer = currentNumbers
					currentNumbers = ""
					if len(dialer.DialTone) > 0 {
						speaker.Play(&toneStreamer{
							sampleRate:  float64(sampleRate),
							frequencies: dialer.DialTone,
						})
						dialTone = true
					}
				}
			}

			dialer := config.Dialers[currentDialer]

			if dialer.Type == "client" {
				number := currentNumbers
				dial := false

				if dialer.Format == "phone" {
					number, err := phonenumbers.Parse(currentNumbers, dialer.Region)
					if err == nil && phonenumbers.IsValidNumber(number) {
						dial = true
					}
				} else {
					matches := regexp.MustCompile(dialer.Format).FindStringSubmatch(currentNumbers)

					if len(matches) == 2 {
						number = matches[1]
						dial = true
					} else if len(matches) == 1 {
						dial = true
					}
				}

				if dial {
					previousDialer := currentDialer
					currentDialer = "invalid"

					if client, ok := clients[dialer.Client]; ok {
						clientUsingPhone = dialer.Client
						client.Call(number, previousDialer)
					} else {
						speaker.Clear()
						speaker.Play(&toneStreamer{
							sampleRate:  float64(sampleRate),
							frequencies: []float64{480, 620},
							onOff:       [2]int{sampleRate / 2, sampleRate / 2},
						})
					}
				}
			}

			if dialer.Type == "map" {
				if data, ok := dialer.Numbers[currentNumbers]; ok {
					previousDialer := currentDialer
					currentDialer = "invalid"

					if client, ok := clients[data[0]]; ok {
						clientUsingPhone = data[0]
						client.Call(data[1], previousDialer)
					} else {
						speaker.Clear()
						speaker.Play(&toneStreamer{
							sampleRate:  float64(sampleRate),
							frequencies: []float64{480, 620},
							onOff:       [2]int{sampleRate / 2, sampleRate / 2},
						})
					}
				}
			}
		}

		mu.Unlock()
	}
}
