package masque

// SegmentStats is the Q36 attribution surface for the MASQUE data path.
//
// FromDevice is what the device handed to the tunnel; ToDevice is what the tunnel returned and the
// device accepted. The two drop counters are this owner's local drops (no session, session not
// ready) and are the only points where this layer discards a datagram itself. Queue-full drops are
// the device OutboundQueue's counters, which Q16 inventories; they are not duplicated here.
type SegmentStats struct {
	DatagramsFromDevice       uint64
	DatagramsToDevice         uint64
	DatagramsDroppedNoSession uint64
	DatagramsDroppedNotReady  uint64
}

func (c *Client) SegmentStats() SegmentStats {
	if c == nil {
		return SegmentStats{}
	}
	return SegmentStats{
		DatagramsFromDevice:       c.datagramsFromDevice.Load(),
		DatagramsToDevice:         c.datagramsToDevice.Load(),
		DatagramsDroppedNoSession: c.datagramsDroppedNoSession.Load(),
		DatagramsDroppedNotReady:  c.datagramsDroppedNotReady.Load(),
	}
}
