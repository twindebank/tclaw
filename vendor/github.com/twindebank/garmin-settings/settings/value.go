package settings

import (
	"encoding/json"
	"fmt"
	"time"
)

// Class is Garmin's discriminator for the shape of a SettingValue. It must be sent back on writes;
// omitting it makes the server reject the value.
type Class string

const (
	// ClassScalar is the ordinary case: an int, string, bool or map value.
	ClassScalar Class = ".SettingValue"

	// ClassDataFieldPage carries an activity data screen in its PageDTO.
	ClassDataFieldPage Class = ".DataFieldPageSettingValue"

	// ClassDeprecatedScreen carries the legacy on-watch screen loop. Newer devices return it with
	// an empty ExecutionScreens; it is not the way to configure data screens.
	ClassDeprecatedScreen Class = ".DeprecatedScreenSettingValue"
)

// AttributeID names a qualifier that scopes a setting to a narrower target than the device — which
// sport, which screen, and so on. A SettingValue is identified by its ID *and* its attributes: two
// DATA_FIELD_PAGES values differ only by these.
type AttributeID string

const (
	AttributeSport     AttributeID = "GARMIN_SPORT"
	AttributeSubSport  AttributeID = "GARMIN_SUBSPORT"
	AttributePage      AttributeID = "DATA_FIELD_PAGE"
	AttributeSettingID AttributeID = "SETTING_ID"
)

// Attribute is one qualifier on a SettingValue.
type Attribute struct {
	ID          AttributeID `json:"id"`
	IntValue    *int        `json:"intValue"`
	LongValue   *int64      `json:"longValue"`
	StringValue *string     `json:"stringValue"`
}

// IntAttribute builds an integer-valued attribute.
func IntAttribute(id AttributeID, value int) Attribute {
	return Attribute{ID: id, IntValue: &value}
}

// Int returns the integer value and whether it was set.
func (a Attribute) Int() (int, bool) {
	if a.IntValue == nil {
		return 0, false
	}
	return *a.IntValue, true
}

// PageDTO holds the fields shown on one data screen, in slot order.
type PageDTO struct {
	DisplayFields []Field `json:"displayFields"`
}

// Value is a single setting. Garmin uses a wide struct with one populated slot rather than a
// polymorphic value, so most pointers are nil on any given value; the typed accessors below are
// the intended way to read it.
type Value struct {
	Class          Class       `json:"class"`
	ID             ID          `json:"settingId"`
	TypeAttributes []Attribute `json:"settingTypeAttributes,omitempty"`

	IntValue     *int              `json:"intValue,omitempty"`
	LongValue    *int64            `json:"longValue,omitempty"`
	FloatValue   *float32          `json:"floatValue,omitempty"`
	DoubleValue  *float64          `json:"doubleValue,omitempty"`
	StringValues []string          `json:"stringValues,omitempty"`
	BooleanValue *bool             `json:"booleanValue,omitempty"`
	DateValue    *string           `json:"dateValue,omitempty"`
	StringMap    map[string]string `json:"stringMap,omitempty"`

	// PageDTO is populated when Class is ClassDataFieldPage.
	PageDTO *PageDTO `json:"pageDTO,omitempty"`

	// DefaultSetting reports whether this is Garmin's default rather than a user choice.
	DefaultSetting bool `json:"defaultSetting,omitempty"`

	// UpdatedDate is server-assigned; it is ignored on write.
	UpdatedDate string `json:"updatedDate,omitempty"`
}

// Int returns the integer value and whether it was set.
func (v Value) Int() (int, bool) {
	if v.IntValue == nil {
		return 0, false
	}
	return *v.IntValue, true
}

// Bool returns the boolean value and whether it was set.
func (v Value) Bool() (bool, bool) {
	if v.BooleanValue == nil {
		return false, false
	}
	return *v.BooleanValue, true
}

// Float returns the double value and whether it was set.
func (v Value) Float() (float64, bool) {
	if v.DoubleValue == nil {
		return 0, false
	}
	return *v.DoubleValue, true
}

// String returns the first string value and whether one was set. Garmin models even single-valued
// string settings as a list.
func (v Value) String() (string, bool) {
	if len(v.StringValues) == 0 {
		return "", false
	}
	return v.StringValues[0], true
}

// Attribute returns the named attribute and whether it was present.
func (v Value) Attribute(id AttributeID) (Attribute, bool) {
	for _, attribute := range v.TypeAttributes {
		if attribute.ID == id {
			return attribute, true
		}
	}
	return Attribute{}, false
}

// UpdatedAt parses UpdatedDate. Garmin returns a naive local timestamp with no zone.
func (v Value) UpdatedAt() (time.Time, error) {
	if v.UpdatedDate == "" {
		return time.Time{}, fmt.Errorf("value %s has no updated date", v.ID)
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, v.UpdatedDate); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("value %s has unparseable updated date %q", v.ID, v.UpdatedDate)
}

// Constructors for the value shapes the API accepts. Each sets Class explicitly because the server
// requires the discriminator on write.

// IntValueOf builds an integer setting.
func IntValueOf(id ID, value int) Value {
	return Value{Class: ClassScalar, ID: id, IntValue: &value}
}

// BoolValueOf builds a boolean setting.
func BoolValueOf(id ID, value bool) Value {
	return Value{Class: ClassScalar, ID: id, BooleanValue: &value}
}

// StringValueOf builds a string setting.
//
// Note that Garmin does **not** validate these server-side — writing "0" to TIME_FORMAT is accepted
// — so callers are responsible for sending a meaningful value.
func StringValueOf(id ID, value string) Value {
	return Value{Class: ClassScalar, ID: id, StringValues: []string{value}}
}

// FloatValueOf builds a double-valued setting.
func FloatValueOf(id ID, value float64) Value {
	return Value{Class: ClassScalar, ID: id, DoubleValue: &value}
}

// Document is the payload exchanged with the settings endpoint.
type Document struct {
	ApplicationKey string  `json:"applicationKey"`
	DeviceID       int64   `json:"deviceId"`
	Values         []Value `json:"settingValues"`
}

// Find returns the first value with the given ID.
func (d Document) Find(id ID) (Value, bool) {
	for _, value := range d.Values {
		if value.ID == id {
			return value, true
		}
	}
	return Value{}, false
}

// FindAll returns every value with the given ID. IDs repeat when values are scoped by attributes —
// one DATA_FIELD_PAGES per sport and page, for instance.
func (d Document) FindAll(id ID) []Value {
	var found []Value
	for _, value := range d.Values {
		if value.ID == id {
			found = append(found, value)
		}
	}
	return found
}

// MarshalJSON is defined so a nil Values slice encodes as [] rather than null, which the server
// rejects.
func (d Document) MarshalJSON() ([]byte, error) {
	type alias Document
	if d.Values == nil {
		d.Values = []Value{}
	}
	return json.Marshal(alias(d))
}
