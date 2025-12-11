package structures

type AcceptLeaderFinalizationProofRequest struct {
	LeaderFinalizations []AggregatedLeaderFinalizationProof `json:"leaderFinalizations"`
}
