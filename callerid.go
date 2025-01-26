package main

import (
	"bytes"
	"fmt"
	"reflect"
	"time"

	"github.com/gopxl/beep/v2/speaker"
)

type calleridData struct {
	Time             time.Time `json:"time" id:"1"`
	Number           string    `json:"number" id:"2"`
	NumberNotPresent string    `json:"numberNotPresent" id:"4"` // O | P
	Name             string    `json:"name" id:"7"`
	NameNotPresent   string    `name:"string" id:"8"` // O | P
}

func calleridDataToBytes(data calleridData) []byte {
	var b bytes.Buffer

	t := reflect.TypeOf(data)
	v := reflect.ValueOf(data)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		if !field.IsExported() {
			continue
		}

		if value.IsZero() {
			continue
		}

		id := field.Tag.Get("id")[0] - 48

		b.WriteByte(id)

		var val string

		if time, ok := value.Interface().(time.Time); ok {
			time = time.Local()

			month := fmt.Sprint(int(time.Month()))
			if len(month) == 1 {
				month = "0" + month
			}

			day := fmt.Sprint(int(time.Day()))
			if len(day) == 1 {
				day = "0" + day
			}

			hour := fmt.Sprint(int(time.Hour()))
			if len(hour) == 1 {
				hour = "0" + hour
			}

			minute := fmt.Sprint(int(time.Minute()))
			if len(minute) == 1 {
				minute = "0" + minute
			}

			val = fmt.Sprintf("%s%s%s%s", month, day, hour, minute)
		} else {
			val = value.Interface().(string)
		}

		b.WriteByte(byte(len(val)))
		b.WriteString(val)
	}

	finalBytes := b.Bytes()

	payload := append([]byte{0x80, byte(len(finalBytes))}, finalBytes...)

	var checksum byte
	for _, b := range payload {
		checksum += b
	}

	return append(payload, -checksum)
}

func callerid(data calleridData) error {
	if data.Time.IsZero() {
		data.Time = time.Now()
	}

	audioMu.Lock()

	streamer, err := newCallerIdStreamer(data)
	if err != nil {
		return err
	}

	speaker.PlayAndWait(streamer)

	audioMu.Unlock()

	return nil
}
