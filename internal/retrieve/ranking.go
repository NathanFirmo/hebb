package retrieve

type Signals struct {
	VectorScore                float64
	AssociationScore           float64
	SalienceStrengthConfidence float64
	FreshnessOrTemporalFit     float64
}

func Score(s Signals) float64 {
	return 0.55*s.VectorScore +
		0.20*s.AssociationScore +
		0.15*s.SalienceStrengthConfidence +
		0.10*s.FreshnessOrTemporalFit
}
