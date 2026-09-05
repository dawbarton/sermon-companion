package app

import (
	"context"
	"testing"
	"time"
)

func TestShutdownCancelsAndWaitsForExportJobs(t *testing.T) {
	server := &Server{jobs: map[string]context.CancelFunc{}}
	jobContext, cancelJob := context.WithCancel(context.Background())
	server.jobs["service"] = cancelJob
	server.jobsWG.Add(1)
	finished := make(chan struct{})
	go func() {
		<-jobContext.Done()
		server.finishExportJob("service")
		close(finished)
	}()

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before the export job finished")
	}
	if !server.closing || len(server.jobs) != 0 {
		t.Fatalf("shutdown state: closing=%v jobs=%d", server.closing, len(server.jobs))
	}
}
