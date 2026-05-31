package memory

import "time"

type TraceKind string

const (
	TraceFact            TraceKind = "fact"
	TraceObservation     TraceKind = "observation"
	TraceDecision        TraceKind = "decision"
	TracePreference      TraceKind = "preference"
	TraceProcedure       TraceKind = "procedure"
	TraceEvent           TraceKind = "event"
	TraceEpisode         TraceKind = "episode"
	TraceSemanticSummary TraceKind = "semantic_summary"
	TraceWarning         TraceKind = "warning"
	TraceQuestion        TraceKind = "question"
)

type TraceStatus string

const (
	StatusActive       TraceStatus = "active"
	StatusInhibited    TraceStatus = "inhibited"
	StatusConsolidated TraceStatus = "consolidated"
	StatusArchived     TraceStatus = "archived"
	StatusForgotten    TraceStatus = "forgotten"
)

type Trace struct {
	ID             int64
	Kind           TraceKind
	Title          string
	Body           string
	Scope          string
	Source         string
	Valence        float64
	Confidence     float64
	Strength       float64
	Salience       float64
	Status         TraceStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastRecalledAt *time.Time
	RecallCount    int64
	MetadataJSON   string
}

type Entity struct {
	ID            int64
	Name          string
	Type          string
	CanonicalName string
	MetadataJSON  string
}

type Association struct {
	ID               int64
	FromTraceID      int64
	ToTraceID        int64
	Relation         string
	Strength         float64
	Confidence       float64
	EvidenceCount    int64
	LastReinforcedAt *time.Time
}
