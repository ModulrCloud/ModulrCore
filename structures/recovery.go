package structures

type RecoveryData struct {
	LastEpochIndex     int     `json:"lastEpochIndex"`
	LastAbsoluteHeight int64   `json:"lastAbsoluteHeight"`
	Genesis            Genesis `json:"genesis"`
	TeamSig            string  `json:"teamSig"`
}
