package event

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const StartedDescription = "dora5:stage_started"

type StatusKind string

const (
	StatusStarted   StatusKind = "started"
	StatusExposure  StatusKind = "exposure"
	StatusCompleted StatusKind = "completed"
)

type StatusFact struct {
	Kind           StatusKind
	Result         string
	ReleaseChanged *bool
}

func ExposureDescription(changed bool) string {
	return "dora5:production_exposure;changed=" + strconv.FormatBool(changed)
}

func CompletionDescription(result string, changed *bool) string {
	description := "dora5:stage_completed:" + result
	if changed != nil {
		description += ";changed=" + strconv.FormatBool(*changed)
	}
	return description
}

func ParseStatusDescription(description string) (StatusFact, error) {
	if description == StartedDescription {
		return StatusFact{Kind: StatusStarted}, nil
	}
	parts := strings.Split(description, ";")
	if len(parts) > 2 {
		return StatusFact{}, errors.New("invalid DORA 5 status description")
	}
	fact := StatusFact{}
	switch {
	case parts[0] == "dora5:production_exposure":
		fact.Kind = StatusExposure
	case strings.HasPrefix(parts[0], "dora5:stage_completed:"):
		fact.Kind = StatusCompleted
		fact.Result = strings.TrimPrefix(parts[0], "dora5:stage_completed:")
		if fact.Result == "" {
			return StatusFact{}, errors.New("completion result is empty")
		}
	default:
		return StatusFact{}, errors.New("not a DORA 5 status description")
	}
	if len(parts) == 2 {
		const prefix = "changed="
		if !strings.HasPrefix(parts[1], prefix) {
			return StatusFact{}, errors.New("invalid DORA 5 status attribute")
		}
		changed, err := strconv.ParseBool(strings.TrimPrefix(parts[1], prefix))
		if err != nil {
			return StatusFact{}, fmt.Errorf("invalid changed attribute: %w", err)
		}
		fact.ReleaseChanged = &changed
	}
	return fact, nil
}
