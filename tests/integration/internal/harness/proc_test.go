package harness

import (
	"runtime"
	"strings"
	"testing"
)

const (
	procSnapshotRecords               = 200
	procSnapshotReadyLine             = "{\"message\":\"ready\"}\n"
	procSnapshotErrorLine             = "err\n"
	procSnapshotEventCountErrorFormat = "snapshot contains %d events, want at most %d"
	procSnapshotErrorCountErrorFormat = "snapshot contains %d stderr lines, want at most %d"
	procFinalEventCountErrorFormat    = "final snapshot contains %d events, want %d"
	procFinalErrorCountErrorFormat    = "final snapshot contains %d stderr lines, want %d"
)

func TestProcCapturedOutputCanSnapshotLiveWriter(t *testing.T) {
	p := &Proc{stdout: &synchronizedBuffer{}, stderr: &synchronizedBuffer{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range procSnapshotRecords {
			_, _ = p.stdout.Write([]byte(procSnapshotReadyLine))
			_, _ = p.stderr.Write([]byte(procSnapshotErrorLine))
			runtime.Gosched()
		}
	}()
	for {
		stdout, stderr := p.CapturedOutput()
		if got := len(ParseEvents(stdout)); got > procSnapshotRecords {
			t.Fatalf(procSnapshotEventCountErrorFormat, got, procSnapshotRecords)
		}
		if got := strings.Count(stderr, procSnapshotErrorLine); got > procSnapshotRecords {
			t.Fatalf(procSnapshotErrorCountErrorFormat, got, procSnapshotRecords)
		}
		select {
		case <-done:
			stdout, stderr = p.CapturedOutput()
			if got := len(ParseEvents(stdout)); got != procSnapshotRecords {
				t.Fatalf(procFinalEventCountErrorFormat, got, procSnapshotRecords)
			}
			if got := strings.Count(stderr, procSnapshotErrorLine); got != procSnapshotRecords {
				t.Fatalf(procFinalErrorCountErrorFormat, got, procSnapshotRecords)
			}
			return
		default:
			runtime.Gosched()
		}
	}
}
