package plugin

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	RunWorkerIfRequested()
	os.Exit(m.Run())
}
