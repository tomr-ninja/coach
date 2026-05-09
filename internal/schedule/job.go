package schedule

import (
	"fmt"

	"github.com/tomr-ninja/coach/internal/wrap"
	"github.com/tomr-ninja/coach/protocol"
)

// BuildJobParams holds the inputs needed to assemble a protocol.Job.
type BuildJobParams struct {
	ImageDigest  string
	ModelImage   string
	DataSource   string
	OutputURI    string
	ScheduleCron string
	Wrapped      bool
	Model        protocol.Model
	Resources    protocol.Resources
	Labels       map[string]string
}

// BuildJob assembles a protocol.Job from its parts.
func BuildJob(p BuildJobParams) *protocol.Job {
	name := fmt.Sprintf("coach-container-runner-%s", wrap.SanitizeImageName(p.ModelImage))
	job := &protocol.Job{
		ImageDigest: p.ImageDigest,
		Name:        name,
		IsRecurring: p.ScheduleCron != "",
		IsWrapped:   p.Wrapped,
		Model:       p.Model,
		Data: protocol.Data{
			Sources:   []string{p.DataSource},
			MountPath: "/data",
		},
		Output: protocol.Output{
			Destination: p.OutputURI,
			MountPath:   "/output",
		},
		Resources: p.Resources,
		Labels:    p.Labels,
	}

	if p.ScheduleCron != "" {
		job.Schedule = &protocol.Schedule{Cron: p.ScheduleCron, Timezone: "UTC"}
	}

	return job
}
