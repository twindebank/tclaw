package settings

import "sort"

// Field is a data field that can occupy a slot on an activity data screen.
//
// Unlike most Garmin string settings, this one *is* validated server-side: an unrecognised value
// makes the write fail with HTTP 500 rather than being silently stored. Field is still a string
// type rather than a closed enum because KnownFields below is what has been confirmed empirically,
// not Garmin's complete list — so a caller may legitimately need one that is not named here.
type Field string

// FieldNone leaves a slot empty; FieldUndefined is what Garmin returns for a slot the device has
// not configured. They are distinct values in the API and are not interchangeable.
const (
	FieldNone      Field = "NONE"
	FieldUndefined Field = "UNDEFINED"
)

// Commonly used fields, named for convenience.
const (
	FieldTime           Field = "TIME"
	FieldTimeElapsed    Field = "TIME_ELAPSED"
	FieldTimeOfDay      Field = "TIME_OF_DAY"
	FieldTimeLap        Field = "TIME_LAP"
	FieldDistance       Field = "DISTANCE"
	FieldDistanceLap    Field = "DISTANCE_LAP"
	FieldPace           Field = "PACE"
	FieldPaceAvg        Field = "PACE_AVG"
	FieldPaceLap        Field = "PACE_LAP"
	FieldSpeed          Field = "SPEED"
	FieldSpeedAvg       Field = "SPEED_AVG"
	FieldHeartRate      Field = "HEART_RATE"
	FieldHeartRateAvg   Field = "HEART_RATE_AVG"
	FieldHeartRateLap   Field = "HEART_RATE_LAP"
	FieldCadence        Field = "CADENCE"
	FieldCadenceAvg     Field = "CADENCE_AVG"
	FieldPower          Field = "POWER"
	FieldPowerAvg       Field = "POWER_AVG"
	FieldPowerLap       Field = "POWER_LAP"
	FieldCalories       Field = "CALORIES"
	FieldElevation      Field = "ELEVATION"
	FieldTotalAscent    Field = "TOTAL_ASCENT"
	FieldTotalDescent   Field = "TOTAL_DESCENT"
	FieldGrade          Field = "GRADE"
	FieldTemperature    Field = "TEMPERATURE"
	FieldLaps           Field = "LAPS"
	FieldSteps          Field = "STEPS"
	FieldTrainingEffect Field = "TRAINING_EFFECT"
)

// knownFields are the values confirmed to be accepted by the API, established by probing each name
// and keeping the ones the server did not reject. It is a lower bound on the real enum.
var knownFields = map[Field]struct{}{
	"AMBIENT_PRESSURE": {}, "ASCENT_LAP": {}, "BEARING": {}, "CADENCE": {}, "CADENCE_AVG": {},
	"CADENCE_LAP": {}, "CALORIES": {}, "COMPASS": {}, "DATE": {}, "DESCENT_LAP": {},
	"DISTANCE": {}, "DISTANCE_LAP": {}, "DISTANCE_REMAINING": {}, "DIVE_TIME": {},
	"ELEVATION": {}, "FLOORS_CLIMBED": {}, "FLOORS_DESCENDED": {}, "FLOW_SCORE": {},
	"GEARS": {}, "GEAR_RATIO": {}, "GLIDE_RATIO": {}, "GPS_ACCURACY": {}, "GRADE": {},
	"GROUND_CONTACT_TIME": {}, "HEADING": {}, "HEART_RATE": {}, "HEART_RATE_AVG": {},
	"HEART_RATE_GRAPH": {}, "HEART_RATE_GRAPH_LITE": {}, "HEART_RATE_LAP": {},
	"HEART_RATE_PERCENT_MAX": {}, "LAPS": {}, "LEFT_RIGHT_BALANCE": {}, "LENGTHS": {},
	"MULTISPORT_TIME": {}, "NAV_DIST_TO_DESTINATION": {}, "NAV_DIST_TO_NEXT": {},
	"NAV_ETA_AT_DESTINATION": {}, "NAV_ETA_AT_NEXT": {}, "NAV_TIME_TO_DESTINATION": {},
	"NAV_TIME_TO_NEXT": {}, "NDL": {}, "NONE": {}, "PACE": {}, "PACE_AVG": {}, "PACE_LAP": {},
	"PACE_LAST_LAP": {}, "PEDAL_SMOOTHNESS": {}, "PERFORMANCE_CONDITION": {}, "POWER": {},
	"POWER_AVG": {}, "POWER_LAP": {}, "POWER_PERCENT_FTP": {}, "POWER_ZONE": {}, "SPEED": {},
	"SPEED_AVG": {}, "SPEED_GRAPH_LITE": {}, "SPEED_LAP": {}, "SPEED_LAST_LAP": {},
	"SPEED_MAX": {}, "STEPS": {}, "STRESS": {}, "STROKE_RATE": {}, "SUNRISE": {}, "SUNSET": {},
	"SURFACE_INTERVAL": {}, "TEMPERATURE": {}, "TEMPERATURE_AVG": {}, "TIME": {},
	"TIME_ELAPSED": {}, "TIME_LAP": {}, "TIME_LAST_LAP": {}, "TIME_OF_DAY": {},
	"TIME_SEATED": {}, "TIME_TO_DEPLETION": {}, "TORQUE_EFFECTIVENESS": {}, "TOTAL_ASCENT": {},
	"TOTAL_DESCENT": {}, "TRAINING_EFFECT": {}, "UNDEFINED": {}, "VERTICAL_OSCILLATION": {},
	"VERTICAL_RATIO": {}, "VERTICAL_SPEED": {},
}

// Known reports whether the field is one confirmed to be accepted.
//
// A false result is not proof the field is invalid — knownFields is a lower bound — so callers
// should treat this as a warning, not a hard gate. The server is the authority and rejects genuinely
// bad values with HTTP 500.
func (f Field) Known() bool {
	_, ok := knownFields[f]
	return ok
}

// KnownFields returns every confirmed field name, sorted. Useful for CLI help and for offering
// choices to a caller that needs to pick one.
func KnownFields() []Field {
	fields := make([]Field, 0, len(knownFields))
	for field := range knownFields {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i] < fields[j] })
	return fields
}
