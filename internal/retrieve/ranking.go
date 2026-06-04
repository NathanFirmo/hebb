package retrieve

type Signals struct {
	VectorScore                float64
	FTSScore                   float64
	AssociationScore           float64
	SalienceStrengthConfidence float64
	FreshnessOrTemporalFit     float64
}

func Score(s Signals) float64 {
	return 0.40*s.VectorScore +
		0.35*s.FTSScore +
		0.10*s.AssociationScore +
		0.10*s.SalienceStrengthConfidence +
		0.05*s.FreshnessOrTemporalFit
}
