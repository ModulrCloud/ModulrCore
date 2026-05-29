package structures

type RecoveryData struct {
	LastEpochIndex     int     `json:"lastEpochIndex"`
	LastAbsoluteHeight int64   `json:"lastAbsoluteHeight"`
	Genesis            Genesis `json:"genesis"`
	TeamSig            string  `json:"teamSig"`
}

type RecoveryGenesisTemplatePayload struct {
	SourceEpochId   int    `json:"sourceEpochId"`
	SourceEpochHash string `json:"sourceEpochHash"`

	CoreMajorVersion  int                `json:"coreMajorVersion"`
	NetworkParameters NetworkParameters  `json:"networkParameters"`
	Validators        []ValidatorStorage `json:"validators"`
}
