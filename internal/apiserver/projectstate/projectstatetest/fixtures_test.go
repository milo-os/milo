package projectstatetest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// slowFirstResync holds the reflector in the view's first resync: after its
// first List, before it opens its watch. A busy machine can hold it there too.
type slowFirstResync struct{ once sync.Once }

func (*slowFirstResync) ProjectGone(string) {}

func (l *slowFirstResync) ViewResynced() {
	l.once.Do(func() { time.Sleep(200 * time.Millisecond) })
}

// TestStart_ViewHearsADeleteMadeAsSoonAsStartReturns covers the fake client's
// gap: it sends an event only to the watches open at the time of the write,
// and it never sends a delete again. So a Project deleted as soon as Start
// returns leaves the view only if the watch is open by then.
func TestStart_ViewHearsADeleteMadeAsSoonAsStartReturns(t *testing.T) {
	client := projectstatetest.NewClient(t, projectstatetest.NewProjectWithUID("a", "uid-a"))
	c := projectstate.NewCache(client)
	c.AddListener(&slowFirstResync{})
	projectstatetest.Start(t, c, client)

	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for _, held := c.Project("a"); held; _, held = c.Project("a") {
		if time.Now().After(deadline) {
			t.Fatal("the view still holds a, deleted as soon as Start returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
