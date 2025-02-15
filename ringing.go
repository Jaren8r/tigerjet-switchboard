package main

import (
	"slices"

	"github.com/google/uuid"
)

var ringing ringingList

type ringingList []ringData

type ringData struct {
	ID         string        `json:"id"`
	CallerID   *calleridData `json:"callerId,omitempty"`
	clientType string
	clientId   uuid.UUID
	devices    []*device
}

func (d ringData) Number() string {
	if d.CallerID != nil {
		return d.CallerID.Number
	}

	return ""
}

func (list *ringingList) StartRinging(ringData ringData) {
	number := ringData.Number()

	for _, d := range devices {
		if d.shouldRing(ringData.clientType, number) {
			ringData.devices = append(ringData.devices, d)

			if !d.ringing && !d.inUse {
				d.ring(ringData.CallerID)
			}

			d.ringing = true
		}
	}

	*list = append(*list, ringData)
}

func (list *ringingList) stopRinging(i int) {
	ringData := (*list)[i]

	*list = append((*list)[:i], (*list)[i+1:]...)

	for _, d := range ringData.devices {
		_, ringIndex := list.Ringing(d)
		if ringIndex == -1 {
			d.ringing = false
			if !d.inUse {
				d.stopRinging()
			}
		}
	}
}

func (list *ringingList) StopRinging(id string) {
	for i := range *list {
		if (*list)[i].ID == id {
			list.stopRinging(i)
			break
		}
	}
}

func (list *ringingList) Ringing(d *device) (ringData, int) {
	for i, ringData := range *list {
		if slices.Contains(ringData.devices, d) {
			return ringData, i
		}
	}

	return ringData{}, -1
}

func (list *ringingList) Answer(d *device) (ringData, bool) {
	for i, ringData := range *list {
		if slices.Contains(ringData.devices, d) {
			list.stopRinging(i)
			return ringData, true
		}
	}

	return ringData{}, false
}
