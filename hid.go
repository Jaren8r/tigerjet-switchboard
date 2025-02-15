package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
)

var startRingingReport = []byte{0x0, 0x20, 0x0, 0x0, 0x1, 0x1, 0x03, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}
var stopRingingReport = []byte{0x0, 0x20, 0x0, 0x0, 0x1, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}

var startRingingSilverReport = []byte{0x0, 0x4, 0x3d, 0x20, 0x1, 0x30, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}
var stopRingingSilverReport = []byte{0x0, 0x4, 0x3d, 0x20, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}
var resetSilverReport = []byte{0x0, 0x4, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}
var reduceRingerInsensitySilverReport = []byte{0x0, 0x4, 0x2f, 0x40, 0x1, 0x14, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}

func (d *device) startRinging() {
	slog.Debug(fmt.Sprintf("[%s] Started ringing", d.serial))

	if d.silver {
		ctx, cancel := context.WithCancel(context.Background())

		d.stopSilverRinger = cancel

		go func() {
		loop:
			for {
				d.sendHidSyncedFeatureReport <- startRingingSilverReport

				select {
				case <-time.After(2 * time.Second):
					d.sendHidSyncedFeatureReport <- stopRingingSilverReport
				case <-ctx.Done():
					d.sendHidSyncedFeatureReport <- stopRingingSilverReport
					break loop
				}

				select {
				case <-time.After(4 * time.Second):
					// NOOP
				case <-ctx.Done():
					break loop
				}
			}
		}()
	} else {
		d.hid.SendFeatureReport(startRingingReport)
	}
}

func (d *device) stopRinging() {
	slog.Debug(fmt.Sprintf("[%s] Stopped ringing", d.serial))

	if d.stopCallerID != nil {
		d.stopCallerID()
		d.stopCallerID = nil
	}

	if !d.silver {
		d.hid.SendFeatureReport(stopRingingReport)
	} else if d.stopSilverRinger != nil {
		d.stopSilverRinger()
		d.stopSilverRinger = nil
	}
}

func (d *device) call(clientType string, number string) {
	previousDialer := d.dialer
	d.dialer = ""

	if client, ok := clients[clientType]; ok {
		if !client.InUse() {
			d.clientUsingPhone = clientType
			client.Call(d, callData{
				Number: number,
				Device: d.audioDeviceIds,
			}, previousDialer)
			slog.Info(fmt.Sprintf("[%s] Calling %s on client %s via dialer %s", d.serial, number, clientType, previousDialer))
		} else {
			slog.Info(fmt.Sprintf("[%s] Calling %s on client %s via dialer %s failed because the client is busy", d.serial, number, clientType, previousDialer))
			d.audio.Play(&toneSource{
				frequencies: busyFrequencies,
				onOff:       busyOnOff,
			})
		}
	} else {
		slog.Info(fmt.Sprintf("[%s] Calling %s on client %s via dialer %s failed because the client does not exist", d.serial, number, clientType, previousDialer))
		d.audio.Play(&toneSource{
			frequencies: busyFrequencies,
			onOff:       busyOnOff,
		})
	}
}

func (d *device) onHidChange(offHook bool, currentNumber byte) {
	mu.Lock()

	if offHook {
		if !d.inUse {
			d.inUse = true

			slog.Debug(fmt.Sprintf("[%s] Off-hook", d.serial))

			d.stopRinging()

			ringData, ok := ringing.Answer(d)

			if ok {
				if client, ok := clients[ringData.clientType]; ok {
					d.clientUsingPhone = ringData.clientType
					d.dialer = ""
					client.Answer(d, callAnswerData{
						ID:       ringData.ID,
						Device:   d.audioDeviceIds,
						ringData: ringData,
					})
					slog.Info(fmt.Sprintf("[%s] Answering call from client %s", d.serial, ringData.clientType))
				}
			}

			if d.clientUsingPhone == "" {
				d.dialer = d.config().Dialer

				d.audio.Play(&toneSource{
					frequencies: config.Dialers[d.dialer].DialTone,
				})

				d.dialTone = true
			}
		}
	} else if d.inUse {
		d.inUse = false
		d.dialpad = ""
		d.dialer = ""
		d.dialTone = false

		d.audio.Stop()

		slog.Debug(fmt.Sprintf("[%s] On-hook", d.serial))

		if d.clientUsingPhone != "" {
			clients[d.clientUsingPhone].End(d)

			d.clientUsingPhone = ""
		}

		ringData, i := ringing.Ringing(d)
		if i > -1 {
			d.ring(ringData.CallerID)
		}
	}

	if currentNumber > 0 && d.dialer != "" {
		if d.dialTone {
			d.audio.Stop()
			d.dialTone = false
		}

		var currentNumberStr string

		switch currentNumber {
		case 11:
			currentNumberStr = "*"
		case 12:
			currentNumberStr = "#"
		default:
			currentNumberStr = strconv.Itoa(int(currentNumber - 1))
		}

		d.dialpad += currentNumberStr

		slog.Debug(fmt.Sprintf("[%s] Dialpad: %s", d.serial, currentNumberStr))

		if d.dialer == "default" {
			if dialer, ok := config.Dialers[d.dialpad]; ok {
				d.dialer = d.dialpad
				d.dialpad = ""
				if len(dialer.DialTone) > 0 {
					d.audio.Play(&toneSource{
						frequencies: dialer.DialTone,
					})
					d.dialTone = true
				}
			}
		}

		dialer := config.Dialers[d.dialer]

		if dialer.Client != "" {
			number := d.dialpad
			dial := false

			if dialer.ClientNumberFormat == "phone" {
				number, err := phonenumbers.Parse(d.dialpad, dialer.ClientNumberRegion)
				if err == nil && phonenumbers.IsValidNumber(number) {
					dial = true
				}
			} else {
				matches := regexp.MustCompile(dialer.ClientNumberFormat).FindStringSubmatch(d.dialpad)

				if len(matches) == 2 {
					number = matches[1]
					dial = true
				} else if len(matches) == 1 {
					dial = true
				}
			}

			if dial {
				d.call(dialer.Client, number)
			}
		}

		if dialer.Map != nil {
			if data, ok := dialer.Map[d.dialpad]; ok {
				d.call(data[0], data[1])
			}
		}
	}

	mu.Unlock()
}

func readFeatureReport(featureReport []byte) (bool, byte) {
	offHook := featureReport[24]&128 == 128
	number := byte(0)

	if featureReport[23] == 1 {
		number = (featureReport[24] & 127) + 1
		if number == 11 {
			number = 1
		} else if number > 11 {
			number -= 1
		}
	}

	return offHook, number
}

func readSilverFeatureReport(featureReport []byte) (bool, byte) {
	offHook := featureReport[10] == 128
	number := byte(0)

	if featureReport[22]&192 == 192 {
		number = (featureReport[22] ^ 192) + 1
		if number == 11 {
			number = 1
		} else if number > 11 {
			number -= 1
		}
	}

	return offHook, number
}

func (d *device) hidInit() {
	var featureReport [65]byte
	var sendHidSyncedFeatureReport chan []byte

	_, err := d.hid.GetFeatureReport(featureReport[:])
	if err != nil {
		// older magicjack devices need to be requested with a 32 byte buffer
		_, err := d.hid.GetFeatureReport(featureReport[:33])
		if err != nil {
			panic(err)
		}

		d.silver = true
		sendHidSyncedFeatureReport = make(chan []byte)
		d.sendHidSyncedFeatureReport = sendHidSyncedFeatureReport
		// if not sent, the ringer overpowers the off-hook detection
		d.hid.SendFeatureReport(reduceRingerInsensitySilverReport)
		d.hid.SendFeatureReport(resetSilverReport)
	} else {
		// some devices require the stop ringing payload to be sent to activate
		_, err := d.hid.SendFeatureReport(stopRingingReport)
		if err != nil {
			panic(err)
		}
	}

	go func() {
		if d.silver {
			currentOffHook, currentNumber := readSilverFeatureReport(featureReport[:])
			d.onHidChange(currentOffHook, currentNumber)

			for {
				select {
				case silverFeatureReport := <-sendHidSyncedFeatureReport:
					// commands need to be done in sync with the hid loop as they change the response of GetFeatureReport
					d.hid.SendFeatureReport(silverFeatureReport)
					d.hid.SendFeatureReport(resetSilverReport)
				default:
					_, err := d.hid.GetFeatureReport(featureReport[:33])
					if err != nil {
						// darwin sometimes reports temporary general errors
						if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "(0xE00002BC)") {
							continue
						}

						panic(err)
					}

					offHook, number := readSilverFeatureReport(featureReport[:])

					if currentOffHook != offHook || currentNumber != number {
						d.onHidChange(offHook, number)

						currentOffHook = offHook
						currentNumber = number
					}
				}
			}
		} else {
			offHook, number := readFeatureReport(featureReport[:])
			d.onHidChange(offHook, number)

			var bytes [2]byte
			for {
				_, err := d.hid.Read(bytes[:])
				if err != nil {
					panic(err)
				}

				d.onHidChange(bytes[1] == 128, bytes[0])
			}
		}
	}()
}
