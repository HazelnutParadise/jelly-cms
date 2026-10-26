package main

import (
	"context"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/server"
)

// startSystemRuntime explicitly starts the durable background services once
// the HTTP listener is bound. Order expiry and post rerender still use their
// existing lifecycle until they are migrated to queue jobs.
func startSystemRuntime(parent context.Context) func() {
	server.SetSystemDatabase(data.DB)
	stopServices := server.StartSystemServices(parent)
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	start := func(worker func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			worker(ctx)
		}()
	}
	start(runOrderExpiry)
	start(server.RerenderStalePosts)
	return func() {
		cancel()
		workers.Wait()
		stopServices()
	}
}
