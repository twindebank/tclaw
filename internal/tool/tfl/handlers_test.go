package tfl

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSummariseJourneyResponse(t *testing.T) {
	t.Run("extracts up to 3 journeys with key fields", func(t *testing.T) {
		raw := json.RawMessage(`{
			"journeys": [
				{
					"duration": 35,
					"arrivalDateTime": "2026-04-12T09:45:00",
					"legs": [
						{"duration": 5, "mode": {"name": "walking"}, "instruction": {"summary": "Walk to bus stop"}},
						{"duration": 25, "mode": {"name": "bus"}, "instruction": {"summary": "Take bus 8 towards Holborn"}},
						{"duration": 5, "mode": {"name": "walking"}, "instruction": {"summary": "Walk to destination"}}
					]
				},
				{
					"duration": 42,
					"arrivalDateTime": "2026-04-12T09:52:00",
					"legs": [
						{"duration": 10, "mode": {"name": "walking"}, "instruction": {"summary": "Walk to Bethnal Green"}},
						{"duration": 12, "mode": {"name": "tube"}, "instruction": {"summary": "Take Central line towards Ealing Broadway"}},
						{"duration": 20, "mode": {"name": "walking"}, "instruction": {"summary": "Walk to office"}}
					]
				}
			]
		}`)

		result, err := summariseJourneyResponse(raw)
		require.NoError(t, err)

		var got journeySummary
		require.NoError(t, json.Unmarshal(result, &got))

		require.Len(t, got.Journeys, 2)

		first := got.Journeys[0]
		require.Equal(t, 35, first.DurationMinutes)
		require.Equal(t, "09:45", first.ArrivalTime)
		require.Len(t, first.Legs, 3)
		require.Equal(t, "bus", first.Legs[1].Mode)
		require.Equal(t, "Take bus 8 towards Holborn", first.Legs[1].Summary)
		require.Equal(t, 25, first.Legs[1].DurationMinutes)
	})

	t.Run("truncates to 3 journeys when more are returned", func(t *testing.T) {
		raw := json.RawMessage(`{
			"journeys": [
				{"duration": 10, "arrivalDateTime": "2026-04-12T10:10:00", "legs": []},
				{"duration": 15, "arrivalDateTime": "2026-04-12T10:15:00", "legs": []},
				{"duration": 20, "arrivalDateTime": "2026-04-12T10:20:00", "legs": []},
				{"duration": 25, "arrivalDateTime": "2026-04-12T10:25:00", "legs": []},
				{"duration": 30, "arrivalDateTime": "2026-04-12T10:30:00", "legs": []}
			]
		}`)

		result, err := summariseJourneyResponse(raw)
		require.NoError(t, err)

		var got journeySummary
		require.NoError(t, json.Unmarshal(result, &got))

		require.Len(t, got.Journeys, 3)
		require.Equal(t, 10, got.Journeys[0].DurationMinutes)
		require.Equal(t, 20, got.Journeys[2].DurationMinutes)
	})

	t.Run("returns empty journeys list when response has none", func(t *testing.T) {
		raw := json.RawMessage(`{"journeys": []}`)

		result, err := summariseJourneyResponse(raw)
		require.NoError(t, err)

		var got journeySummary
		require.NoError(t, json.Unmarshal(result, &got))

		require.Empty(t, got.Journeys)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, err := summariseJourneyResponse(json.RawMessage(`not json`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse journey response")
	})

	t.Run("handles short arrivalDateTime gracefully", func(t *testing.T) {
		// If the API returns a truncated or unexpected datetime, don't panic.
		raw := json.RawMessage(`{
			"journeys": [
				{"duration": 10, "arrivalDateTime": "short", "legs": []}
			]
		}`)

		result, err := summariseJourneyResponse(raw)
		require.NoError(t, err)

		var got journeySummary
		require.NoError(t, json.Unmarshal(result, &got))

		require.Len(t, got.Journeys, 1)
		require.Equal(t, "short", got.Journeys[0].ArrivalTime)
	})
}
