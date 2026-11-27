package route

import (
	"io"

	B "github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// growthBuffersForDirection reports how many grown buffers one copy direction will
// hold, which is what the growth budget must charge.
//
// Two things decide this, and both are the copy engine's own:
//
//  1. The vectorised loop is entered only when a vectorised read waiter and a
//     vectorised writer can be created for the operands the engine ends up with.
//     It allocates B.DefaultBatchSize grown buffers per read and the writer holds the
//     whole batch until it has been written, so one direction costs that many units.
//
//  2. The engine strips the counting and replaceable wrapper layers BEFORE it decides:
//     sing's copyWithIncreateBuffer unwraps ReadCounter/WriteCounter and
//     ReaderWithUpstream/WriterWithUpstream first (common/bufio/copy.go), so probing the
//     wrapped objects would answer for a layer the engine never reads or writes. The
//     tracker's counting connection and the sniffing cached connection are exactly such
//     layers; probing the raw arguments said "single buffer" while the engine then
//     vectorised and held eight (MD-005).
//
// The probe runs once per copy direction at setup, never on the data path. A direction
// whose writer is a scheduler gate wrapper may be over-charged, which is the safe side.
func growthBuffersForDirection(source io.Reader, destination io.Writer) int {
	unwrappedSource, _ := N.UnwrapCountReader(source, nil)
	unwrappedDestination, _ := N.UnwrapCountWriter(destination, nil)
	if _, ok := B.CreateVectorisedReadWaiter(unwrappedSource); !ok {
		return 1
	}
	if _, ok := B.CreateVectorisedWriter(unwrappedDestination); !ok {
		return 1
	}
	return B.DefaultBatchSize
}

var _ = io.Discard
