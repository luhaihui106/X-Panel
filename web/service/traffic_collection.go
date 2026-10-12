package service

import (
	"sync"
	"x-ui/xray"
)

type trafficDrain func() ([]*xray.Traffic, []*xray.ClientTraffic, error)
type trafficBatch struct {
	inbound []*xray.Traffic
	clients []*xray.ClientTraffic
}
type trafficCollector struct {
	mu      sync.Mutex
	pending *trafficBatch
}

var scheduledTraffic trafficCollector

// CollectTraffic serializes the destructive read through the committed reset.
// Failed batches stay in memory and are retried before any new read. A process
// crash between the core read and SQLite commit can still lose that batch:
// Xray's destructive read has no atomic durable acknowledgement protocol.
func (s *InboundService) CollectTraffic(drain func() ([]*xray.Traffic, []*xray.ClientTraffic, error), committed ...func([]*xray.Traffic, []*xray.ClientTraffic)) (error, bool) {
	return scheduledTraffic.collect(s, drain, committed...)
}
func (c *trafficCollector) collect(s *InboundService, drain trafficDrain, committed ...func([]*xray.Traffic, []*xray.ClientTraffic)) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		inbound, clients, err := drain()
		if err != nil {
			return err, false
		}
		c.pending = &trafficBatch{inbound, clients}
	}
	err, restart := s.storeTraffic(c.pending.inbound, c.pending.clients, true)
	if err != nil {
		return err, false
	}
	batch := c.pending
	c.pending = nil
	for _, notify := range committed {
		notify(batch.inbound, batch.clients)
	}
	return nil, restart
}
