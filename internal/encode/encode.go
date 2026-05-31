package encode

import (
	"errors"
	"strings"

	"github.com/nathan/hebb/internal/memory"
)

type Request struct {
	Kind   memory.TraceKind
	Title  string
	Body   string
	Scope  string
	Source string
}

func Normalize(req Request) (memory.Trace, error) {
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" && body == "" {
		return memory.Trace{}, errors.New("trace title or body is required")
	}
	kind := req.Kind
	if kind == "" {
		kind = memory.TraceObservation
	}
	return memory.Trace{
		Kind:       kind,
		Title:      title,
		Body:       body,
		Scope:      strings.TrimSpace(req.Scope),
		Source:     strings.TrimSpace(req.Source),
		Confidence: 0.7,
		Strength:   0.5,
		Salience:   0.5,
		Status:     memory.StatusActive,
	}, nil
}
