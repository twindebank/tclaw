// Package settings models Garmin Connect's typed device-settings API.
//
// Every setting is a SettingValue keyed by a namespaced ID (for example
// "DeviceSettingId.TIME_FORMAT"). Writes are deltas: sending one SettingValue leaves the rest of
// the device's settings untouched, so nothing here needs to read-modify-write a whole document.
package settings

import "strings"

// Namespace groups setting IDs by what they apply to.
type Namespace string

const (
	// NamespaceDevice covers per-device preferences: units, formats, backlight, notifications.
	NamespaceDevice Namespace = "DeviceSettingId"

	// NamespaceSport covers per-activity settings, including data screens and training zones.
	NamespaceSport Namespace = "SportSettingId"

	// NamespaceUser covers account-level values shared across devices: height, weight, sleep times.
	NamespaceUser Namespace = "UserSettingId"

	// NamespaceAlarm covers alarms.
	NamespaceAlarm Namespace = "AlarmSettingId"

	// NamespaceDerived covers values Garmin computes rather than accepts, such as age from birthday.
	NamespaceDerived Namespace = "DerivedSettingId"

	// NamespaceExecution covers the on-watch screen loop. Its SCREENS value is served with class
	// ".DeprecatedScreenSettingValue" on newer devices; prefer DataScreen for data screens.
	NamespaceExecution Namespace = "ExecutionSettingId"

	// NamespaceStatefuleness carries deletion tombstones (Garmin's spelling, kept for fidelity).
	NamespaceStatefuleness Namespace = "StatefulenessSettingId"
)

// ID is a fully-qualified setting identifier, such as "DeviceSettingId.TIME_FORMAT".
type ID string

// Namespace returns the portion before the dot.
func (id ID) Namespace() Namespace {
	name, _, found := strings.Cut(string(id), ".")
	if !found {
		return ""
	}
	return Namespace(name)
}

// Name returns the portion after the dot.
func (id ID) Name() string {
	_, name, found := strings.Cut(string(id), ".")
	if !found {
		return string(id)
	}
	return name
}

// newID joins a namespace and a name.
func newID(namespace Namespace, name string) ID { return ID(string(namespace) + "." + name) }

// Named constants for frequently-used settings. These are a convenience for Go callers; the full
// set of 131 known settings lives in the catalogue (see Catalogue, Search and Resolve), and any
// setting can be addressed by ID whether or not it is named here.
//
// ID is a string type rather than a closed enum because the catalogue is a lower bound on what
// Garmin defines — an unlisted setting still works.
const (
	TimeFormat              ID = "DeviceSettingId.TIME_FORMAT"
	DateFormat              ID = "DeviceSettingId.DATE_FORMAT"
	MeasurementUnits        ID = "DeviceSettingId.MEASUREMENT_UNITS"
	DistanceUnit            ID = "DeviceSettingId.DISTANCE_UNIT"
	ElevationUnit           ID = "DeviceSettingId.ELEVATION_UNIT"
	HeightUnit              ID = "DeviceSettingId.HEIGHT_UNIT"
	WeightUnit              ID = "DeviceSettingId.WEIGHT_UNIT"
	TemperatureUnit         ID = "DeviceSettingId.TEMPERATURE_UNIT"
	PaceSpeedUnit           ID = "DeviceSettingId.PACE_SPEED_UNIT"
	BacklightMode           ID = "DeviceSettingId.BACKLIGHT_MODE"
	MountingSide            ID = "DeviceSettingId.MOUNTING_SIDE"
	StartOfWeek             ID = "DeviceSettingId.START_OF_WEEK"
	Language                ID = "DeviceSettingId.LANGUAGE"
	DoNotDisturbEnabled     ID = "DeviceSettingId.DND_ENABLED"
	LiveTrackEnabled        ID = "DeviceSettingId.LIVE_TRACK_ENABLED"
	AutoSyncStepsBeforeSync ID = "DeviceSettingId.AUTO_SYNC_STEPS_BEFORE_SYNC"
	AutoSyncMinsBeforeSync  ID = "DeviceSettingId.AUTO_SYNC_MINUTES_BEFORE_SYNC"
	KeyTonesEnabled         ID = "DeviceSettingId.KEY_TONES_ENABLED"
	KeyVibrationEnabled     ID = "DeviceSettingId.KEY_VIBRATION_ENABLED"
	AlertTonesEnabled       ID = "DeviceSettingId.ALERT_TONES_ENABLED"
	OpticalHeartRateEnabled ID = "DeviceSettingId.OPTICAL_HEART_RATE_ENABLED"

	// DataFieldPages and DataFieldPagesNumZones together describe one activity data screen. Use the
	// DataScreen helpers rather than these directly — they require matching TypeAttributes.
	DataFieldPages        ID = "SportSettingId.DATA_FIELD_PAGES"
	DataFieldPagesNumZone ID = "SportSettingId.DATA_FIELD_PAGES_NUM_ZONES"
)
