package settings

import "fmt"

// Sport is a FIT `sport` enum value. Garmin scopes per-activity settings by sport and sub-sport,
// using the numbers from the public FIT profile rather than names.
type Sport int

const (
	SportGeneric          Sport = 0
	SportRunning          Sport = 1
	SportCycling          Sport = 2
	SportTransition       Sport = 3
	SportFitnessEquipment Sport = 4
	SportSwimming         Sport = 5
	SportBasketball       Sport = 6
	SportSoccer           Sport = 7
	SportTennis           Sport = 8
	SportAmericanFootball Sport = 9
	SportTraining         Sport = 10
	SportWalking          Sport = 11
	SportCrossCountrySki  Sport = 12
	SportAlpineSki        Sport = 13
	SportSnowboarding     Sport = 14
	SportRowing           Sport = 15
	SportMountaineering   Sport = 16
	SportHiking           Sport = 17
	SportMultisport       Sport = 18
	SportPaddling         Sport = 19
)

var sportNames = map[Sport]string{
	SportGeneric: "generic", SportRunning: "running", SportCycling: "cycling",
	SportTransition: "transition", SportFitnessEquipment: "fitness_equipment",
	SportSwimming: "swimming", SportBasketball: "basketball", SportSoccer: "soccer",
	SportTennis: "tennis", SportAmericanFootball: "american_football", SportTraining: "training",
	SportWalking: "walking", SportCrossCountrySki: "cross_country_skiing",
	SportAlpineSki: "alpine_skiing", SportSnowboarding: "snowboarding", SportRowing: "rowing",
	SportMountaineering: "mountaineering", SportHiking: "hiking", SportMultisport: "multisport",
	SportPaddling: "paddling",
}

// String returns the FIT name, or a numeric form for values not in the table above. Unknown values
// are rendered rather than rejected: the FIT sport enum is longer than the subset named here, and a
// device may legitimately use one.
func (s Sport) String() string {
	if name, ok := sportNames[s]; ok {
		return name
	}
	return fmt.Sprintf("sport(%d)", int(s))
}

// ParseSport resolves a FIT sport name. It accepts the names String produces.
func ParseSport(name string) (Sport, error) {
	for sport, known := range sportNames {
		if known == name {
			return sport, nil
		}
	}
	return 0, fmt.Errorf("unknown sport %q", name)
}

// SubSport is a FIT `sub_sport` enum value. Sub-sports are only meaningful alongside a Sport —
// sub-sport 7 is road cycling but also has an unrelated meaning under other sports.
type SubSport int

const (
	SubSportGeneric      SubSport = 0
	SubSportTreadmill    SubSport = 1
	SubSportStreet       SubSport = 2
	SubSportTrail        SubSport = 3
	SubSportTrack        SubSport = 4
	SubSportSpin         SubSport = 5
	SubSportIndoorCycle  SubSport = 6
	SubSportRoad         SubSport = 7
	SubSportMountain     SubSport = 8
	SubSportDownhill     SubSport = 9
	SubSportRecumbent    SubSport = 10
	SubSportCyclocross   SubSport = 11
	SubSportHandCycling  SubSport = 12
	SubSportTrackCycling SubSport = 13
	SubSportIndoorRowing SubSport = 14
	SubSportElliptical   SubSport = 15
	SubSportStairStepper SubSport = 16
	SubSportLapSwimming  SubSport = 17
	SubSportOpenWater    SubSport = 18
)

// Activity identifies the profile a data screen belongs to.
type Activity struct {
	Sport    Sport
	SubSport SubSport
}

// String renders the pair for logs and CLI output.
func (a Activity) String() string {
	return fmt.Sprintf("%s/%d", a.Sport, int(a.SubSport))
}
