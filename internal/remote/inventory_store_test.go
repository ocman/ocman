package remote

import (
	"sync"
	"testing"
)

func TestManagerStoreConcurrentReadsShareState(t *testing.T) {
	store := newStateDB(t)
	id, err := store.AddRemote(t.Context(), "127.0.0.1:59998", "token", "Saved remote")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			_, err := store.GetRemote(t.Context(), id)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
