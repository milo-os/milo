package initializer_test

import (
	"testing"

	"go.miloapis.com/milo/internal/apiserver/admission/initializer"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/projectsuspension"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// TestProjectStatesInitializerInjectsTheSharedView covers the one path by
// which the suspension plugin gets its view of Projects: without it the
// plugin fails its initialization, and the server does not start.
func TestProjectStatesInitializerInjectsTheSharedView(t *testing.T) {
	p, err := projectsuspension.NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	if err := p.ValidateInitialization(); err == nil {
		t.Fatal("plugin validated without a view of Projects")
	}

	states, _ := projectstatetest.NewCache(t)
	initializer.ProjectStatesInitializer{States: states}.Initialize(p)
	if err := p.ValidateInitialization(); err != nil {
		t.Fatalf("plugin did not receive the view of Projects: %v", err)
	}
}
