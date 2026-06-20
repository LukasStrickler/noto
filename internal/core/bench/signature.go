package bench

// CheckHypothesisSignature validates required movers for gated hypotheses (§12.15).
func CheckHypothesisSignature(hypothesisID string, base, cand RunBundle, waterfall map[string]float64) *SignatureCheck {
	check := &SignatureCheck{
		HypothesisID: hypothesisID,
		Pass:         true,
	}
	switch hypothesisID {
	case "H1", "":
		if hypothesisID == "" {
			check.Pass = true
			return check
		}
		check = checkH1(base, cand, waterfall)
	default:
		check.Pass = true
	}
	return check
}

func checkH1(base, cand RunBundle, waterfall map[string]float64) *SignatureCheck {
	check := &SignatureCheck{HypothesisID: "H1", Pass: true}

	bSpeech, _ := base.Metrics.Denominators["speech_hours"].(float64)
	cSpeech, _ := cand.Metrics.Denominators["speech_hours"].(float64)
	var speechPct float64
	if bSpeech > 0 {
		speechPct = (cSpeech - bSpeech) / bSpeech * 100
	}
	speechPass := speechPct <= -3
	check.RequiredMovers = append(check.RequiredMovers, SignatureMover{
		Field: "metrics.denominators.speech_hours", Direction: "down",
		MinPct: 3, ActualPct: speechPct, Pass: speechPass,
	})
	if !speechPass {
		check.Pass = false
	}

	diarDelta := waterfall["diar_emb"]
	diarPass := diarDelta < -0.01
	check.RequiredMovers = append(check.RequiredMovers, SignatureMover{
		Field: "cost.waterfall_deltas_usd.diar_emb", Direction: "down",
		MinUSD: 0.01, Actual: diarDelta, Pass: diarPass,
	})
	if !diarPass {
		check.Pass = false
	}

	if warm, ok := waterfall["warm_idle"]; ok && warm < -0.001 && diarDelta >= 0 {
		check.ForbiddenHits = append(check.ForbiddenHits, "cost_down_only_via_warm_idle")
		check.Pass = false
	}
	return check
}

// RejectGateKnobBundle returns true when gate tier has multiple knobs without integration_only.
func RejectGateKnobBundle(m RunManifest) bool {
	if m.IntegrationOnly || m.Tier == "confirm" || m.Tier == "baseline_variance" {
		return false
	}
	return len(m.Knobs) > 1
}
