package settings

import (
	"context"
	"fmt"
	"net/http"

	"github.com/twindebank/garmin-settings/garmin"
)

// Client reads and writes device settings.
type Client struct {
	api *garmin.Client
}

// NewClient returns a settings client over an authenticated API client.
func NewClient(api *garmin.Client) *Client {
	return &Client{api: api}
}

func devicePath(id garmin.DeviceID) string {
	return fmt.Sprintf("/devicesettings-service/device/%d", id)
}

// Get returns every setting the device currently has.
//
// Only settings that have been *set* appear. A device with no configured data screens simply has no
// DATA_FIELD_PAGES values — which is not the same as the device being unable to hold them.
func (c *Client) Get(ctx context.Context, id garmin.DeviceID) (Document, error) {
	var document Document
	err := c.api.DoJSON(ctx, garmin.Request{
		Method: http.MethodGet,
		Path:   devicePath(id),
	}, &document)
	if err != nil {
		return Document{}, fmt.Errorf("get settings for device %d: %w", id, err)
	}
	return document, nil
}

// SetParams describes a settings write.
type SetParams struct {
	DeviceID       garmin.DeviceID
	ApplicationKey garmin.ApplicationKey
	Values         []Value
}

// Set writes settings and returns the server's view of the result.
//
// Writes are deltas: values not named here are left alone, so there is no need to send the whole
// document. Two consequences worth knowing:
//
//   - A setting that is currently unset cannot be returned to unset. Writing one is a one-way door,
//     so only write settings the caller actually intends to manage.
//   - String settings are not validated server-side; a nonsense value is stored happily. Data
//     screen fields *are* validated and fail the whole request with HTTP 500.
func (c *Client) Set(ctx context.Context, params SetParams) (Document, error) {
	if len(params.Values) == 0 {
		return Document{}, fmt.Errorf("set settings: no values supplied")
	}
	if params.ApplicationKey == "" {
		return Document{}, fmt.Errorf("set settings: application key is required")
	}
	for _, value := range params.Values {
		if value.Class == "" {
			return Document{}, fmt.Errorf("set settings: value %s has no class", value.ID)
		}
		if value.ID == "" {
			return Document{}, fmt.Errorf("set settings: a value has no setting id")
		}
	}

	payload := Document{
		ApplicationKey: string(params.ApplicationKey),
		DeviceID:       int64(params.DeviceID),
		Values:         params.Values,
	}

	var result Document
	err := c.api.DoJSON(ctx, garmin.Request{
		Method: http.MethodPut,
		Path:   devicePath(params.DeviceID),
		Body:   payload,
	}, &result)
	if err != nil {
		return Document{}, fmt.Errorf("set settings for device %d: %w", params.DeviceID, err)
	}
	return result, nil
}
