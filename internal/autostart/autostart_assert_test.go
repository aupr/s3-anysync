package autostart_test

import (
	"github.com/aupr/s3-anysync/internal/agent"
	"github.com/aupr/s3-anysync/internal/autostart"
)

// autostart.Manager must satisfy agent.Autostart (the agent does not import
// autostart, and autostart does not import agent, so there is no cycle).
var _ agent.Autostart = autostart.Manager(nil)
