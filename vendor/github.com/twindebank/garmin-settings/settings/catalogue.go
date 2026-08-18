package settings

import (
	"sort"
	"strings"
)

// Kind is the value slot a setting uses. Garmin's SettingValue is one wide struct with a single
// populated field, so knowing the kind is what lets a caller build a correct value from a string
// without hard-coding each setting.
type Kind string

const (
	KindInt           Kind = "int"
	KindLong          Kind = "long"
	KindFloat         Kind = "float"
	KindDouble        Kind = "double"
	KindString        Kind = "string"
	KindBool          Kind = "bool"
	KindStringMap     Kind = "stringMap"
	KindDate          Kind = "date"
	KindAlarm         Kind = "alarm"
	KindDataFieldPage Kind = "dataFieldPage"

	// KindUnknown covers settings observed only with an empty value, so their slot is unproven.
	KindUnknown Kind = "unknown"
)

// Definition describes one setting: how to encode its value and how it is addressed.
type Definition struct {
	ID   ID
	Kind Kind

	// Scoped marks settings that exist once per activity rather than once per device, and so
	// require GARMIN_SPORT / GARMIN_SUBSPORT attributes (and a page, for data screens). Writing one
	// without attributes targets the wrong thing.
	Scoped bool

	// ReadOnly marks values Garmin derives rather than accepts. Writing them is pointless at best.
	ReadOnly bool
}

// catalogue is every setting observed across real devices (fenix 8 Pro, fenix 6X Pro, Edge 1040).
// It is a lower bound on what Garmin defines, not the closed set: a device may expose settings not
// listed here, which is why ID stays a string type and unknown IDs pass through.
var catalogue = buildCatalogue()

func buildCatalogue() map[ID]Definition {
	definitions := make(map[ID]Definition)

	add := func(namespace Namespace, kind Kind, scoped, readOnly bool, names ...string) {
		for _, name := range names {
			id := newID(namespace, name)
			definitions[id] = Definition{ID: id, Kind: kind, Scoped: scoped, ReadOnly: readOnly}
		}
	}

	add(NamespaceDevice, KindBool, false, false,
		"ACTIVITY_TRACKING_ENABLED", "ALERT_TONES_ENABLED", "AUDIO_PROMPT_ACTIVITY_ALERTS_ENABLED",
		"AUDIO_PROMPT_HEART_RATE_ENABLED", "AUDIO_PROMPT_LAP_ENABLED", "AUDIO_PROMPT_POWER_ENABLED",
		"AUDIO_PROMPT_SPEED_PACE_ENABLED", "AUTO_ACTIVITY_DETECT_ENABLED", "AUTO_UPDATE",
		"AUTO_UPLOAD_ENABLED", "BLE_CONNECTION_ALERT_ENABLED", "DND_ENABLED",
		"HIGH_HR_ALERT_ENABLED", "KEY_TONES_ENABLED", "KEY_VIBRATION_ENABLED",
		"LIVE_EVENT_SHARING_ENABLED", "LIVE_TRACK_ENABLED", "LOW_HR_ALERT_ENABLED",
		"METRICS_FILE_TRUEUP_ENABLED", "MOVE_ALERT_ENABLED", "OPTICAL_HEART_RATE_ENABLED",
		"PULSE_OX_ACCLIMATION_ENABLED", "PULSE_OX_SLEEP_TRACKING_ENABLED",
		"SOUND_IN_APP_ONLY_ENABLED", "SOUND_VIBRATION_ENABLED", "USER_PHONE_VERIFICATION")

	add(NamespaceDevice, KindInt, false, false,
		"AUDIO_PROMPT_HEART_RATE_DURATION", "AUDIO_PROMPT_POWER_DURATION",
		"AUDIO_PROMPT_SPEED_PACE_DURATION", "AUTO_SYNC_MINUTES_BEFORE_SYNC",
		"AUTO_SYNC_STEPS_BEFORE_SYNC", "HIGH_HR_ALERT_THRESHOLD", "LANGUAGE",
		"LOW_HR_ALERT_THRESHOLD")

	add(NamespaceDevice, KindString, false, false,
		"AUDIO_PROMPT_DIALECT_TYPE", "AUDIO_PROMPT_HEART_RATE_FREQUENCY",
		"AUDIO_PROMPT_HEART_RATE_TYPE", "AUDIO_PROMPT_POWER_FREQUENCY", "AUDIO_PROMPT_POWER_TYPE",
		"AUDIO_PROMPT_SPEED_PACE_FREQUENCY", "AUDIO_PROMPT_SPEED_PACE_TYPE", "BACKLIGHT_MODE",
		"DATE_FORMAT", "DISTANCE_UNIT", "ELEVATION_UNIT", "GOAL_ANIMATION", "HEIGHT_UNIT",
		"LIVE_EVENT_SHARING_MSG_CONTENTS", "LIVE_EVENT_SHARING_MSG_TRIGGERS", "MEASUREMENT_UNITS",
		"MOUNTING_SIDE", "PACE_SPEED_UNIT", "PHONE_NOTIFICATION_PRIVACY_MODE",
		"SMART_NOTIFICATIONS_STATUS", "SMART_NOTIFICATION_TIMEOUT", "START_OF_WEEK",
		"TEMPERATURE_UNIT", "TIME_FORMAT", "WEIGHT_UNIT")

	add(NamespaceDevice, KindStringMap, false, false, "CONTROLS_MENU_LIST", "SCHOOL_MODE")

	add(NamespaceSport, KindBool, true, false,
		"AUTO_LAP_ENABLED", "AUTO_PAUSE_ENABLED", "LAP_KEY_ENABLED", "RESTING_HR_AUTO_UPDATE_USED")
	add(NamespaceSport, KindInt, true, false,
		"DATA_FIELD_PAGES_NUM_ZONES", "HR_ZONE1_FLOOR", "HR_ZONE2_FLOOR", "HR_ZONE3_FLOOR",
		"HR_ZONE4_FLOOR", "HR_ZONE5_FLOOR", "LACTATE_THRESHOLD_HEART_RATE_USED",
		"MAX_HEART_RATE_USED", "VIRTUAL_PACER_PACE")
	add(NamespaceSport, KindFloat, true, false,
		"AUTO_LAP_DISTANCE_IN_METERS", "DISTANCE_ALERT_METER", "POOL_LENGTH")
	add(NamespaceSport, KindDouble, true, false,
		"FUNCTIONAL_THRESHOLD_POWER", "POWER_ZONE_ZONE1_FLOOR", "POWER_ZONE_ZONE2_FLOOR",
		"POWER_ZONE_ZONE3_FLOOR", "POWER_ZONE_ZONE4_FLOOR", "POWER_ZONE_ZONE5_FLOOR",
		"POWER_ZONE_ZONE6_FLOOR", "POWER_ZONE_ZONE7_FLOOR")
	add(NamespaceSport, KindString, true, false, "TRAINING_METHOD")
	add(NamespaceSport, KindDataFieldPage, true, false, "DATA_FIELD_PAGES")

	add(NamespaceUser, KindBool, false, false,
		"HYDRATION_AUTO_GOAL_ENABLED", "THRESHOLD_HEART_RATE_AUTO_DETECTED")
	add(NamespaceUser, KindInt, false, false,
		"ADAPTIVE_COACHING_CYCLING_TARGET_TYPE", "ADAPTIVE_COACHING_RUNNING_TARGET_TYPE",
		"DAY_OF_WEEK_SLEEP_WINDOW_SLEEP_TIME", "DAY_OF_WEEK_SLEEP_WINDOW_WAKE_TIME",
		"LACTATE_THRESHOLD_HEART_RATE", "MODERATE_INTENSITY_MINUTES_HR_ZONE",
		"VIGOROUS_INTENSITY_MINUTES_HR_ZONE")
	add(NamespaceUser, KindLong, false, false,
		"DIVE_NUMBER", "EXTERNAL_BOTTOM_TIME", "FIRSTBEAT_RUNNING_LT_TIMESTAMP",
		"GLUCOSE_MEASUREMENT_UNIT", "HYDRATION_MEASUREMENT_UNIT", "PRIMARY_TRAINING_DEVICE",
		"SLEEP_TIME", "WAKE_TIME")
	add(NamespaceUser, KindDouble, false, false,
		"HEIGHT", "LACTATE_THRESHOLD_PACE", "VO2_MAX", "WEIGHT")
	add(NamespaceUser, KindString, false, false,
		"DATE_MODE", "FULL_NAME", "GENDER", "GOLF_DISTANCE_UNIT", "HANDEDNESS",
		"INTENSITY_MINUTES_CALC_METHOD", "MEASUREMENT_SYSTEM", "START_OF_WEEK")
	add(NamespaceUser, KindDate, false, false, "BIRTH_DATE")
	add(NamespaceUser, KindUnknown, false, false, "TIME_MODE")

	add(NamespaceAlarm, KindAlarm, false, false, "ALARMS")
	add(NamespaceAlarm, KindBool, false, false, "MULTIPLE_ALARM_ENABLED")

	// Derived values are computed by Garmin from other settings; writing them has no effect.
	add(NamespaceDerived, KindInt, false, true, "AGE", "BIRTH_DAY", "BIRTH_MONTH", "BIRTH_YEAR")
	add(NamespaceDerived, KindString, false, true, "ALL_UNITS")
	add(NamespaceDerived, KindBool, false, true, "IS_PRIMARY_TRACKER", "PRIMARY_LHA_BACKUP")
	add(NamespaceDerived, KindLong, false, true, "ON_DEVICE_SLEEP_CAPABLE_COUNT")

	add(NamespaceExecution, KindUnknown, false, false, "SCREENS")
	add(NamespaceExecution, KindString, false, false, "SMART_NOTIFICATIONS_STATUS")
	add(NamespaceStatefuleness, KindUnknown, false, true, "DELETED")

	return definitions
}

// Lookup returns the definition for a setting id.
func Lookup(id ID) (Definition, bool) {
	definition, ok := catalogue[id]
	return definition, ok
}

// Catalogue returns every known setting definition, sorted by id.
func Catalogue() []Definition {
	definitions := make([]Definition, 0, len(catalogue))
	for _, definition := range catalogue {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	return definitions
}

// Search returns known settings whose id contains term, case-insensitively. Intended for a caller
// that knows roughly what it wants ("backlight", "unit") but not the exact identifier.
func Search(term string) []Definition {
	term = strings.ToUpper(term)
	var matches []Definition
	for _, definition := range Catalogue() {
		if strings.Contains(strings.ToUpper(string(definition.ID)), term) {
			matches = append(matches, definition)
		}
	}
	return matches
}

// Resolve turns a partial name into a setting id.
//
// It accepts a fully-qualified id ("DeviceSettingId.TIME_FORMAT") or a bare name ("TIME_FORMAT",
// "time_format"). A bare name that matches settings in more than one namespace is ambiguous and
// returns an error listing the candidates rather than silently picking one.
func Resolve(name string) (Definition, error) {
	if definition, ok := Lookup(ID(name)); ok {
		return definition, nil
	}

	wanted := strings.ToUpper(strings.TrimSpace(name))
	var matches []Definition
	for _, definition := range Catalogue() {
		if strings.ToUpper(definition.ID.Name()) == wanted {
			matches = append(matches, definition)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Definition{}, &UnknownSettingError{Name: name}
	default:
		candidates := make([]string, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, string(match.ID))
		}
		return Definition{}, &AmbiguousSettingError{Name: name, Candidates: candidates}
	}
}
