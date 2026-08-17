package garmin

import (
	"context"
	"fmt"
	"net/http"
)

// DeviceID is Garmin's numeric identifier for a registered device. It is also the `serial_number`
// in the settings FIT file.
type DeviceID int64

// ApplicationKey identifies a device *model* (for example "fenix8Pro51mm"), as opposed to DeviceID
// which identifies one physical unit.
type ApplicationKey string

// Device is a device registered to the account.
type Device struct {
	DeviceID        DeviceID       `json:"deviceId"`
	ApplicationKey  ApplicationKey `json:"applicationKey"`
	ProductName     string         `json:"productDisplayName"`
	FirmwareVersion string         `json:"currentFirmwareVersion"`

	// SettingsFile names the device's settings document (RealTimeDeviceSettings_<uuid>.json). No
	// endpoint that serves it has been found; it is exposed here because it identifies the device's
	// settings schema and may become useful.
	SettingsFile string `json:"deviceSettingsFile"`
}

// Devices lists every device registered to the account.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var devices []Device
	err := c.DoJSON(ctx, Request{
		Method: http.MethodGet,
		Path:   "/device-service/deviceregistration/devices",
	}, &devices)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return devices, nil
}

// Device fetches one device by id.
func (c *Client) Device(ctx context.Context, id DeviceID) (Device, error) {
	var device Device
	err := c.DoJSON(ctx, Request{
		Method: http.MethodGet,
		Path:   fmt.Sprintf("/device-service/deviceregistration/devices/%d", id),
	}, &device)
	if err != nil {
		return Device{}, fmt.Errorf("get device %d: %w", id, err)
	}
	return device, nil
}

// SettingsChangeFIT downloads the FIT file the watch fetches to apply pending settings changes.
//
// Garmin regenerates this from the current settings on each request, so it is a useful way to
// confirm that a write actually reached the delivery path. It is served only as binary — asking for
// JSON returns 406.
func (c *Client) SettingsChangeFIT(ctx context.Context, id DeviceID) ([]byte, error) {
	raw, err := c.Do(ctx, Request{
		Method: http.MethodGet,
		Path:   fmt.Sprintf("/devicesettings-service/settings-change/fit/%d", id),
		Accept: "application/octet-stream",
	})
	if err != nil {
		return nil, fmt.Errorf("download settings-change FIT for device %d: %w", id, err)
	}
	return raw, nil
}
