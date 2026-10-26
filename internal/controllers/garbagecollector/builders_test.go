package garbagecollector

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// TestBuildersWalkedWhileProjectsChange walks the graph builders from other goroutines while projects are engaged
// and dropped, and while Sync resyncs every builder (milo#834, F7). It proves the walks safe only under -race.
func TestBuildersWalkedWhileProjectsChange(t *testing.T) {
	g := newTestGC(t, widgets)
	period := 50 * time.Millisecond
	go g.Run(g.ctx, 1, period)
	go g.Sync(g.ctx, g.discovery, period)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				g.IsSynced(klog.Background())
				g.GraphHasUID("absent")
			}
		}
	})

	for i := range 10 {
		g.addProject(t, fmt.Sprintf("p%d", i), widgets)
		if i%2 == 0 {
			g.discovery.setPreferredResources(testResources(widgets, gizmos), nil)
		} else {
			g.discovery.setPreferredResources(testResources(widgets), nil)
		}
		if i > 0 {
			g.RemoveProject(fmt.Sprintf("p%d", i-1))
		}
	}
	close(stop)
	readers.Wait()
}

// TestGraphBuilderRunsOnce starts one builder twice, as the collector's Run and AddProject both can: the second
// start returns at once, so one queue never has two graph-change processors (milo#834, F7).
func TestGraphBuilderRunsOnce(t *testing.T) {
	g := newTestGC(t, widgets)
	gb := g.GetDependencyGraphBuilder()
	go gb.Run(g.ctx)
	if err := wait.PollUntilContextTimeout(g.ctx, 10*time.Millisecond, wait.ForeverTestTimeout, true,
		func(context.Context) (bool, error) { return isRunning(gb), nil }); err != nil {
		t.Fatal("the builder never started")
	}

	returned := make(chan struct{})
	go func() {
		gb.Run(g.ctx)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a second Run of a running builder did not return")
	}
}

func isRunning(gb *GraphBuilder) bool {
	gb.monitorLock.RLock()
	defer gb.monitorLock.RUnlock()
	return gb.running
}
