package protocol

const Version = 1

type Spec struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Type            string `json:"type"`
	ID              string `json:"id,omitempty"`
	Job             *Job   `json:"job,omitempty"`
}

type Job struct {
	Fingerprint string            `json:"fingerprint"`
	Name        string            `json:"name"`
	IsWrapped   bool              `json:"isWrapped"`
	IsRecurring bool              `json:"isRecurring"`
	Schedule    *Schedule         `json:"schedule,omitempty"`
	Model       Model             `json:"model"`
	Data        Data              `json:"data"`
	Output      Output            `json:"output"`
	Resources   Resources         `json:"resources"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type Schedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone,omitempty"`
}

type Model struct {
	Image   string            `json:"image"`
	Command []string          `json:"command,omitempty"`
	Script  string            `json:"script,omitempty"`
	EnvVars map[string]string `json:"envVars,omitempty"`
}

type Data struct {
	Sources   []string `json:"sources"`
	MountPath string   `json:"mountPath"`
}

type Output struct {
	Destination string `json:"destination"`
	MountPath   string `json:"mountPath"`
}

type Resources struct {
	CPUMillicores uint32 `json:"cpuMillicores,omitempty"`
	MemoryMi      uint32 `json:"memoryMi,omitempty"`
	GPU           uint32 `json:"gpu,omitempty"`
	GPUType       string `json:"gpuType,omitempty"`
}

type DriverResult struct {
	Success         bool          `json:"success"`
	Error           string        `json:"error,omitempty"`
	ProtocolVersion int           `json:"protocolVersion"`
	SubmitResult    *SubmitResult `json:"submitResult,omitempty"`
	ListResult      *ListResult   `json:"listResult,omitempty"`
	StatusResult    *StatusResult `json:"statusResult,omitempty"`
}

type SubmitResult struct {
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
}

type ListResult struct {
	Entries []ScheduleEntry `json:"entries"`
}

type StatusResult struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	LastRunAt string `json:"lastRunAt,omitempty"`
	NextRunAt string `json:"nextRunAt,omitempty"`
}

type ScheduleEntry struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule,omitempty"`
	Status   string `json:"status"`
	URL      string `json:"url,omitempty"`
}
