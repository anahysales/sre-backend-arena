package service

import (
	"strconv"
	"strings"
)

// calcula threat score baseado em crew + passengers
func CalculateThreat(crewStr, passengersStr string) (int, string) {

	parse := func(s string) int {
		s = strings.ReplaceAll(s, ",", "")
		val, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return val
	}

	crew := parse(crewStr)
	passengers := parse(passengersStr)

	score := (crew + passengers) / 10000

	if score > 100 {
		score = 100
	}

	// classificação de ameaça
	classification := ""

	switch {
	case score < 20:
		classification = "low_threat"
	case score < 50:
		classification = "medium_threat"
	case score < 80:
		classification = "high_threat"
	default:
		classification = "galactic_superweapon"
	}

	return score, classification
}