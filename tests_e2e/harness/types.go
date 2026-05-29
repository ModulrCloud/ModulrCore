package main

type Manifest struct {
	Name  string         `json:"name"`
	Nodes []ManifestNode `json:"nodes"`
}

type ManifestNode struct {
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	RepoPath      string            `json:"repoPath"`
	WorkDir       string            `json:"workDir,omitempty"`
	Command       []string          `json:"command"`
	ChaindataPath string            `json:"chaindataPath"`
	HealthURL     string            `json:"healthURL,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
}

type GeneratedNetwork struct {
	RunID       string `json:"runId"`
	RootDir     string `json:"rootDir"`
	Manifest    string `json:"manifest"`
	CoreCount   int    `json:"coreCount"`
	AnchorCount int    `json:"anchorCount"`
	CreatedAt   string `json:"createdAt"`
}

type RunState struct {
	RunID       string      `json:"runId"`
	Manifest    string      `json:"manifest"`
	StartedAt   string      `json:"startedAt"`
	RunDir      string      `json:"runDir"`
	LogsDir     string      `json:"logsDir"`
	Nodes       []NodeState `json:"nodes"`
	HarnessNote string      `json:"harnessNote,omitempty"`
}

type NodeState struct {
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	PID           int      `json:"pid"`
	RepoPath      string   `json:"repoPath"`
	WorkDir       string   `json:"workDir"`
	Command       []string `json:"command"`
	ChaindataPath string   `json:"chaindataPath"`
	HealthURL     string   `json:"healthURL,omitempty"`
	StdoutLog     string   `json:"stdoutLog"`
	StderrLog     string   `json:"stderrLog"`
	StartedAt     string   `json:"startedAt"`
}
