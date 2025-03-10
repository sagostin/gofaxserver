package gofaxserver

import (
	"errors"
	"regexp"
	"strconv"
	"time"
)

func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

// ParseDuration converts a string like "1m" or "5m" into a time.Duration
func ParseDuration(input string) (time.Duration, error) {
	re := regexp.MustCompile(`^(\d+)([smhd])$`)
	matches := re.FindStringSubmatch(input)
	if len(matches) != 3 {
		return 0, errors.New("invalid duration format")
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, err
	}

	switch matches[2] {
	case "s":
		return time.Duration(value) * time.Second, nil
	case "m":
		return time.Duration(value) * time.Minute, nil
	case "h":
		return time.Duration(value) * time.Hour, nil
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	default:
		return 0, errors.New("unsupported time unit")
	}
}
