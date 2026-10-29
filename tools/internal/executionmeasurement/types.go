package executionmeasurement

const Schema = "ccme-execution-measurement/v1"

type Reference struct {
	Source     string `json:"source"`
	Pointer    string `json:"pointer"`
	EvidenceID string `json:"evidenceId,omitempty"`
}

type Evidence struct {
	ID     string `json:"id"`
	Path   string `json:"path,omitempty"`
	URI    string `json:"uri,omitempty"`
	SHA256 string `json:"sha256"`
}

type Quantity struct {
	Value    *float64    `json:"value"`
	Evidence []Reference `json:"evidence"`
	Reason   string      `json:"reason,omitempty"`
}

type Trial struct {
	ID                  string            `json:"id"`
	Repetition          int               `json:"repetition"`
	Status              string            `json:"status"`
	Tool                string            `json:"tool"`
	Mode                string            `json:"mode"`
	Profile             string            `json:"profile"`
	Workload            string            `json:"workload"`
	SourceRevision      string            `json:"sourceRevision"`
	BinarySHA256        string            `json:"binarySha256"`
	ProtocolRevision    string            `json:"protocolRevision"`
	ConfigurationSHA256 string            `json:"configurationSha256"`
	Toolchain           map[string]string `json:"toolchain"`
	Evidence            []Evidence        `json:"evidence"`
}

type Vertex struct {
	ID             string      `json:"id"`
	Operation      string      `json:"operation"`
	IsVirtual      bool        `json:"isVirtual"`
	DurationInputs []Reference `json:"durationInputs"`
	DurationRule   string      `json:"durationRule,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type GraphCounts struct {
	Vertices        int `json:"vertices"`
	Edges           int `json:"edges"`
	VirtualVertices int `json:"virtualVertices"`
	TimedVertices   int `json:"timedVertices"`
}

type Graph struct {
	ID          string      `json:"id"`
	Scope       string      `json:"scope"`
	Provenance  string      `json:"provenance"`
	Vertices    []Vertex    `json:"vertices"`
	Edges       []Edge      `json:"edges"`
	Counts      GraphCounts `json:"counts"`
	OmittedWork []string    `json:"omittedWork"`
	Evidence    []Reference `json:"evidence"`
}

type Resources struct {
	Nodes              []map[string]any `json:"nodes"`
	Pools              []map[string]any `json:"pools"`
	TaskSlotLimit      Quantity         `json:"taskSlotLimit"`
	UsableOnePoolSlots Quantity         `json:"usableOnePoolSlots"`
	Nested             []map[string]any `json:"nested"`
}

type TaskInterval struct {
	TaskID          string      `json:"taskId"`
	AttemptID       string      `json:"attemptId"`
	NodeID          string      `json:"nodeId"`
	Status          string      `json:"status"`
	Operation       string      `json:"operation"`
	Boundary        string      `json:"boundary"`
	DurationSeconds *float64    `json:"durationSeconds"`
	ClockDomain     string      `json:"clockDomain"`
	ClockKind       string      `json:"clockKind"`
	StartSeconds    *float64    `json:"startSeconds,omitempty"`
	EndSeconds      *float64    `json:"endSeconds,omitempty"`
	Evidence        []Reference `json:"evidence"`
	Reason          string      `json:"reason,omitempty"`
}

type TimeWindow struct {
	ID               string      `json:"id"`
	Boundary         string      `json:"boundary"`
	Seconds          *float64    `json:"seconds"`
	ClockDomain      string      `json:"clockDomain"`
	ClockKind        string      `json:"clockKind"`
	Evidence         []Reference `json:"evidence"`
	IncludedSteps    []string    `json:"includedSteps,omitempty"`
	Aggregation      string      `json:"aggregation,omitempty"`
	ContainsTasks    []string    `json:"containsTasks,omitempty"`
	ContainmentProof []Reference `json:"containmentEvidence,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

type Calculation struct {
	ID          string               `json:"id"`
	Formula     string               `json:"formula"`
	Inputs      map[string]Reference `json:"inputs"`
	Value       *float64             `json:"value"`
	Unit        string               `json:"unit"`
	Status      string               `json:"status"`
	Reasons     []string             `json:"reasons"`
	Model       string               `json:"model"`
	ClockMethod string               `json:"clockMethod"`
	Path        []string             `json:"criticalPath,omitempty"`
}

type Measurement struct {
	Schema              string         `json:"schema"`
	Trial               Trial          `json:"trial"`
	Graph               Graph          `json:"graph"`
	Resources           Resources      `json:"resources"`
	TaskIntervals       []TaskInterval `json:"taskIntervals"`
	TimeWindows         []TimeWindow   `json:"timeWindows"`
	ObservedPeakOverlap Quantity       `json:"observedPeakOverlap"`
	Analysis            []Calculation  `json:"analysis"`
	ClockCompatibility  map[string]any `json:"clockCompatibility"`
	Premises            map[string]any `json:"premises"`
}
