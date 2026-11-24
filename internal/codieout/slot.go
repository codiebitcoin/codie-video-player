package codieout

import (
	"encoding/json"
	"errors"
	"fmt"
)

// The device keeps character slots: each holds a role — its character, its
// voice — and the screen shows the one whose slot is active. codie_stream_play
// binds roles to slots; this app only switches which one stands there, so the
// right role watches a video.

// ActiveSlot asks which character slot the device shows, its slot_number.
func (c *Client) ActiveSlot() (int, error) {
	body, err := c.Call("GET", "/v1/api/data/user/slot", nil, payloadEmpty, "")
	if err != nil {
		return 0, err
	}
	var ans struct {
		Data struct {
			SlotNumber int `json:"slot_number"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return 0, fmt.Errorf("the device's slot made no sense: %w", err)
	}
	if ans.Data.SlotNumber <= 0 {
		return 0, errors.New("the device answered no slot")
	}
	return ans.Data.SlotNumber, nil
}

// UseSlot switches the device's active character slot: the role bound to it
// stands on the screen from then on.
func (c *Client) UseSlot(slot int) error {
	body, err := json.Marshal(map[string]any{"slot_number": slot})
	if err != nil {
		return err
	}
	answer, err := c.Call("PUT", "/v1/api/data/user/slot", body, payloadJSON, "")
	if err != nil {
		return err
	}
	var ans struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	json.Unmarshal(answer, &ans)
	if !ans.Success {
		return errors.New(first(ans.Message, "the device didn't switch the slot"))
	}
	return nil
}
