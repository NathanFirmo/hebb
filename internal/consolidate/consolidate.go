package consolidate

type Plan struct {
	Scope                string
	MergeDuplicates      bool
	PromoteSemanticFacts bool
	RecalibrateLinks     bool
	ApplyDecay           bool
	RequireHumanReview   bool
}

func ConservativePlan(scope string) Plan {
	return Plan{
		Scope:                scope,
		MergeDuplicates:      true,
		PromoteSemanticFacts: true,
		RecalibrateLinks:     true,
		ApplyDecay:           true,
		RequireHumanReview:   true,
	}
}
