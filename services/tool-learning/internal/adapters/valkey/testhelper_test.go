package valkey

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func startMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}
