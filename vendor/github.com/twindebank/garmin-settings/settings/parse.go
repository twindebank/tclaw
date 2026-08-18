package settings

import (
	"fmt"
	"strconv"
	"strings"
)

// UnknownSettingError reports a name that matches no known setting.
type UnknownSettingError struct {
	Name string
}

func (e *UnknownSettingError) Error() string {
	return fmt.Sprintf("unknown setting %q (try Search to find it)", e.Name)
}

// AmbiguousSettingError reports a bare name that exists in more than one namespace.
type AmbiguousSettingError struct {
	Name       string
	Candidates []string
}

func (e *AmbiguousSettingError) Error() string {
	return fmt.Sprintf("setting %q is ambiguous; qualify it as one of: %s",
		e.Name, strings.Join(e.Candidates, ", "))
}

// UnsupportedKindError reports a setting whose value shape cannot be built from a plain string.
type UnsupportedKindError struct {
	ID   ID
	Kind Kind
}

func (e *UnsupportedKindError) Error() string {
	return fmt.Sprintf("setting %s holds a %s value, which cannot be set from a plain string", e.ID, e.Kind)
}

// ParseValue builds a Value for a setting from its textual form, using the catalogue to decide
// which slot to populate. This is what lets a CLI or an MCP tool accept "set X to Y" generically
// instead of needing a bespoke path per setting.
//
// Structured kinds (alarms, string maps, data screens) are rejected: they need more than one scalar,
// and data screens have SetDataScreen.
func ParseValue(definition Definition, raw string) (Value, error) {
	if definition.ReadOnly {
		return Value{}, fmt.Errorf("setting %s is derived by Garmin and cannot be written", definition.ID)
	}

	switch definition.Kind {
	case KindString:
		// Deliberately unvalidated: Garmin does not constrain these server-side either, and the
		// accepted vocabulary differs per setting and device.
		return StringValueOf(definition.ID, raw), nil

	case KindBool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return Value{}, fmt.Errorf("setting %s wants a boolean, got %q", definition.ID, raw)
		}
		return BoolValueOf(definition.ID, parsed), nil

	case KindInt:
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return Value{}, fmt.Errorf("setting %s wants an integer, got %q", definition.ID, raw)
		}
		return IntValueOf(definition.ID, parsed), nil

	case KindLong:
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("setting %s wants an integer, got %q", definition.ID, raw)
		}
		return Value{Class: ClassScalar, ID: definition.ID, LongValue: &parsed}, nil

	case KindDouble:
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return Value{}, fmt.Errorf("setting %s wants a number, got %q", definition.ID, raw)
		}
		return FloatValueOf(definition.ID, parsed), nil

	case KindFloat:
		parsed, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return Value{}, fmt.Errorf("setting %s wants a number, got %q", definition.ID, raw)
		}
		narrowed := float32(parsed)
		return Value{Class: ClassScalar, ID: definition.ID, FloatValue: &narrowed}, nil

	case KindDate:
		return Value{Class: ClassScalar, ID: definition.ID, DateValue: &raw}, nil

	case KindUnknown:
		// Only ever seen empty, so the slot is a guess. A string is the least surprising attempt and
		// the server rejects it if wrong — better than silently writing the wrong shape.
		return StringValueOf(definition.ID, raw), nil

	default:
		return Value{}, &UnsupportedKindError{ID: definition.ID, Kind: definition.Kind}
	}
}

// ScopeToActivity attaches sport attributes to a value, which per-activity settings require.
func ScopeToActivity(value Value, activity Activity) Value {
	value.TypeAttributes = []Attribute{
		IntAttribute(AttributeSport, int(activity.Sport)),
		IntAttribute(AttributeSubSport, int(activity.SubSport)),
	}
	return value
}
