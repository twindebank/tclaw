package settings

import (
	"context"
	"fmt"

	"github.com/twindebank/garmin-settings/garmin"
)

// DataScreen is one activity data screen: which activity it belongs to, its position in that
// activity's screen loop, and the field in each slot.
//
// In the API this is two SettingValues sharing a set of attributes — DATA_FIELD_PAGES holds the
// fields and DATA_FIELD_PAGES_NUM_ZONES holds the layout. They are modelled as one type here
// because writing only one of them produces an inconsistent screen.
type DataScreen struct {
	Activity Activity

	// Page is the screen's index within the activity, 1-based.
	Page int

	// Fields is the field in each slot, in order.
	Fields []Field

	// Zones is the layout — how many slots the watch renders. Garmin stores it separately from
	// Fields and the two can legitimately differ, so it is not simply len(Fields); see Validate.
	Zones int
}

// String renders the screen for logs and CLI output.
func (s DataScreen) String() string {
	return fmt.Sprintf("%s page %d: %v (%d zones)", s.Activity, s.Page, s.Fields, s.Zones)
}

// Validate reports problems a caller can fix before the request is sent.
//
// Unknown field names are *not* an error: the known-field list is a lower bound on Garmin's real
// enum, and the server rejects genuinely invalid names itself. Use UnknownFields to warn.
func (s DataScreen) Validate() error {
	if s.Page < 1 {
		return fmt.Errorf("data screen page must be 1 or greater, got %d", s.Page)
	}
	if len(s.Fields) == 0 {
		return fmt.Errorf("data screen must have at least one field")
	}
	if s.Zones < 1 {
		return fmt.Errorf("data screen zones must be 1 or greater, got %d", s.Zones)
	}
	if s.Zones > len(s.Fields) {
		return fmt.Errorf("data screen declares %d zones but only %d fields", s.Zones, len(s.Fields))
	}
	return nil
}

// UnknownFields returns fields not on the confirmed list, so a caller can warn before writing.
func (s DataScreen) UnknownFields() []Field {
	var unknown []Field
	for _, field := range s.Fields {
		if !field.Known() {
			unknown = append(unknown, field)
		}
	}
	return unknown
}

// attributes builds the qualifier set that identifies this screen's SettingValues.
func (s DataScreen) attributes() []Attribute {
	return []Attribute{
		IntAttribute(AttributeSport, int(s.Activity.Sport)),
		IntAttribute(AttributeSubSport, int(s.Activity.SubSport)),
		IntAttribute(AttributePage, s.Page),
	}
}

// values renders the screen as the two SettingValues the API expects.
func (s DataScreen) values() []Value {
	attributes := s.attributes()
	zones := s.Zones
	return []Value{
		{
			Class:          ClassDataFieldPage,
			ID:             DataFieldPages,
			TypeAttributes: attributes,
			PageDTO:        &PageDTO{DisplayFields: s.Fields},
		},
		{
			Class:          ClassScalar,
			ID:             DataFieldPagesNumZone,
			TypeAttributes: attributes,
			IntValue:       &zones,
		},
	}
}

// DataScreens extracts every data screen from a settings document.
//
// The two underlying values are matched on their shared attributes. A page whose zone count is
// missing falls back to the field count, which is the common case on devices where only the field
// list was ever written.
func DataScreens(document Document) []DataScreen {
	zonesByKey := make(map[Activity]map[int]int)
	for _, value := range document.FindAll(DataFieldPagesNumZone) {
		activity, page, ok := screenKey(value)
		if !ok {
			continue
		}
		zones, ok := value.Int()
		if !ok {
			continue
		}
		if zonesByKey[activity] == nil {
			zonesByKey[activity] = make(map[int]int)
		}
		zonesByKey[activity][page] = zones
	}

	var screens []DataScreen
	for _, value := range document.FindAll(DataFieldPages) {
		activity, page, ok := screenKey(value)
		if !ok || value.PageDTO == nil {
			continue
		}
		screen := DataScreen{Activity: activity, Page: page, Fields: value.PageDTO.DisplayFields}
		if zones, ok := zonesByKey[activity][page]; ok {
			screen.Zones = zones
		} else {
			screen.Zones = len(screen.Fields)
		}
		screens = append(screens, screen)
	}
	return screens
}

// SetDataScreenParams describes a data screen write.
type SetDataScreenParams struct {
	DeviceID       garmin.DeviceID
	ApplicationKey garmin.ApplicationKey
	Screen         DataScreen
}

// SetDataScreen writes one activity data screen.
//
// This overwrites the screen at that activity and page if one exists. There is no delete: removing
// a screen requires a StatefulenessSettingId.DELETED tombstone against a numeric setting id, and
// that name-to-number mapping is not yet known.
func (c *Client) SetDataScreen(ctx context.Context, params SetDataScreenParams) (DataScreen, error) {
	if err := params.Screen.Validate(); err != nil {
		return DataScreen{}, fmt.Errorf("invalid data screen: %w", err)
	}

	result, err := c.Set(ctx, SetParams{
		DeviceID:       params.DeviceID,
		ApplicationKey: params.ApplicationKey,
		Values:         params.Screen.values(),
	})
	if err != nil {
		return DataScreen{}, fmt.Errorf("write data screen: %w", err)
	}

	// Report back what the server stored rather than what was asked for, so a caller can see any
	// coercion it applied.
	for _, screen := range DataScreens(result) {
		if screen.Activity == params.Screen.Activity && screen.Page == params.Screen.Page {
			return screen, nil
		}
	}
	return DataScreen{}, fmt.Errorf("write data screen: server response did not contain %s page %d",
		params.Screen.Activity, params.Screen.Page)
}

// --- helpers ---

// screenKey pulls the activity and page attributes off a data-screen value.
func screenKey(value Value) (Activity, int, bool) {
	sport, ok := intAttribute(value, AttributeSport)
	if !ok {
		return Activity{}, 0, false
	}
	subSport, ok := intAttribute(value, AttributeSubSport)
	if !ok {
		return Activity{}, 0, false
	}
	page, ok := intAttribute(value, AttributePage)
	if !ok {
		return Activity{}, 0, false
	}
	return Activity{Sport: Sport(sport), SubSport: SubSport(subSport)}, page, true
}

func intAttribute(value Value, id AttributeID) (int, bool) {
	attribute, ok := value.Attribute(id)
	if !ok {
		return 0, false
	}
	return attribute.Int()
}
