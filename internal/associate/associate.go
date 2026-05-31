package associate

import "errors"

type Request struct {
	FromTraceID int64
	ToTraceID   int64
	Relation    string
	Strength    float64
	Confidence  float64
}

func Validate(req Request) error {
	if req.FromTraceID <= 0 || req.ToTraceID <= 0 {
		return errors.New("both trace ids are required")
	}
	if req.FromTraceID == req.ToTraceID {
		return errors.New("cannot associate a trace with itself")
	}
	if req.Relation == "" {
		return errors.New("relation is required")
	}
	return nil
}
