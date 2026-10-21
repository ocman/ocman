package telemetry

import "sync"

// UnregisterCacheSizeGauge drops a cache from the size-gauge
// registry. Mostly useful for tests; production caches are
// process-lived so they're never unregistered.
func UnregisterCacheSizeGauge(name string) {
	cacheSizesMu.Lock()
	delete(cacheSizes, name)
	cacheSizesMu.Unlock()
}

// resetCacheInstrumentsForTests forces re-binding of the cache
// instruments to the current global Meter on the next call. Tests
// that swap the global MeterProvider (e.g. to install a
// ManualReader) must call this so subsequent Record* calls land on
// the test meter rather than the original (no-op or production)
// meter.
//
// Not exported — telemetry is the only package allowed to look
// inside its own once-state.
func resetCacheInstrumentsForTests() {
	cacheInstrumentsOnce = sync.Once{}
	cacheLookups = nil
	cacheEvictions = nil
	cacheSizesMu.Lock()
	cacheSizes = map[string]func() int64{}
	cacheSizesMu.Unlock()
}
