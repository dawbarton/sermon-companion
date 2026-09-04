package app

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
)

type deviceListResponse struct {
	Backend string `json:"backend"`
	// Selectable reports whether the backend produced a list to choose from. A
	// backend that did not is not an error the operator has to act on.
	Selectable   bool             `json:"selectable"`
	Devices      []capture.Device `json:"devices"`
	SelectedID   string           `json:"selectedId"`
	SelectedName string           `json:"selectedName"`
	// SelectedMissing is true only when the devices were listed successfully and
	// the saved one was not among them, which is what happens when the HDMI
	// capture is unplugged or enumerated under a new identifier.
	SelectedMissing bool   `json:"selectedMissing"`
	Recording       bool   `json:"recording"`
	Error           string `json:"error,omitempty"`
}

// listDevices reports the capture devices the operator can choose between,
// alongside the saved choice. The dock asks for this when it loads, so a device
// that disappeared between services is visible before the service starts rather
// than as a failure to begin recording.
func (s *Server) listDevices(w http.ResponseWriter, _ *http.Request) {
	c := s.settings.Get()
	_, _, _, recording := s.capture.Active()
	response := deviceListResponse{Backend: c.Capture.Backend, Devices: []capture.Device{}, SelectedID: c.Capture.DeviceID, SelectedName: c.Capture.Device, Recording: recording}
	devices, err := capture.Available(c)
	if err != nil {
		response.Error = err.Error()
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.Selectable, response.Devices = true, devices
	response.SelectedMissing = !deviceSelectable(devices, c.Capture.DeviceID, c.Capture.Device)
	writeJSON(w, http.StatusOK, response)
}

// selectDevice records the operator's choice in config.json so that it is used
// again next Sunday.
func (s *Server) selectDevice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, _, _, recording := s.capture.Active(); recording {
		writeError(w, http.StatusConflict, errors.New("the capture device cannot be changed while a service is being recorded"))
		return
	}
	devices, err := capture.Available(s.settings.Get())
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	id, name := strings.TrimSpace(request.ID), "default"
	if id != "" {
		match := findDevice(devices, id)
		if match == nil {
			// Refusing here keeps config.json describing a device that exists.
			// Saving an absent identifier would turn a recoverable mistake into
			// a recording that fails to start on Sunday morning.
			writeError(w, http.StatusBadRequest, errors.New("that capture device is no longer available; choose another"))
			return
		}
		id, name = match.ID, match.Name
	}
	updated, err := s.settings.Update(func(c *config.Config) error {
		c.Capture.DeviceID, c.Capture.Device = id, name
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("save the chosen capture device: %w", err))
		return
	}
	log.Printf("capture device set to %q (id %q)", updated.Capture.Device, updated.Capture.DeviceID)
	writeJSON(w, http.StatusOK, map[string]string{"selectedId": updated.Capture.DeviceID, "selectedName": updated.Capture.Device})
}

func findDevice(devices []capture.Device, id string) *capture.Device {
	for index := range devices {
		if strings.EqualFold(devices[index].ID, id) {
			return &devices[index]
		}
	}
	return nil
}

// deviceSelectable mirrors the capture backend's own device lookup, so that the
// dock's judgement of "this device is present" cannot disagree with what
// starting a recording would actually find.
func deviceSelectable(devices []capture.Device, id, name string) bool {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" && (name == "" || strings.EqualFold(name, "default")) {
		return true
	}
	for index := range devices {
		if id != "" && strings.EqualFold(devices[index].ID, id) {
			return true
		}
		if id == "" && strings.EqualFold(devices[index].Name, name) {
			return true
		}
	}
	return false
}
