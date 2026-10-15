package adapter

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"io"
	"net/netip"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

type FakeIPMetadata struct {
	Inet4Range   netip.Prefix
	Inet6Range   netip.Prefix
	Inet4Current netip.Addr
	Inet6Current netip.Addr
}

func (m *FakeIPMetadata) MarshalBinary() (data []byte, err error) {
	var buffer bytes.Buffer
	for _, marshaler := range []encoding.BinaryMarshaler{m.Inet4Range, m.Inet6Range, m.Inet4Current, m.Inet6Current} {
		data, err = marshaler.MarshalBinary()
		if err != nil {
			return
		}
		common.Must(binary.Write(&buffer, binary.BigEndian, uint16(len(data))))
		buffer.Write(data)
	}
	data = buffer.Bytes()
	return
}

// UnmarshalBinary decodes a persisted FakeIP cursor.
//
// # The length prefix is untrusted input, and it is REPORTED rather than raised
//
// The value this decodes comes off disk, so it may be truncated, hand-edited, or written
// by a different version. A missing length prefix is therefore a DATA problem, and this
// method already has the only appropriate answer for one: an error return.
//
// It used to call `common.Must(binary.Read(...))`, which PANICS. A panic here is not
// contained where it happens - it propagates out of the caller's bbolt transaction, and the
// cache file's recovery wrapper answers a panic inside a transaction by deleting the WHOLE
// file (experimental/cachefile/cache.go's `view`/`batch`/`update` call `resetDB`, which
// removes the path). So a three-byte value under one FakeIP key destroyed the DNS cache,
// the rule-set cache, the persisted selector choices and the Clash mode with it - a data
// loss caused by a data problem, in a function that was already able to say "no".
//
// MEASURED before the change: a `fakeip_metadata` value of `00 01 00` (and any truncation
// landing on an element boundary, e.g. the 14-byte IPv4-only blob cut after element 1)
// reached this line with an empty reader, and the whole cache file was gone afterwards.
//
// The same reasoning applies to a length prefix that overruns the value: `io.ReadFull`
// below already reports that as an error rather than allocating on the strength of it.
func (m *FakeIPMetadata) UnmarshalBinary(data []byte) error {
	reader := bytes.NewReader(data)
	for _, unmarshaler := range []encoding.BinaryUnmarshaler{&m.Inet4Range, &m.Inet6Range, &m.Inet4Current, &m.Inet6Current} {
		var length uint16
		err := binary.Read(reader, binary.BigEndian, &length)
		if err != nil {
			return E.Cause(err, "read element length")
		}
		element := make([]byte, length)
		_, err = io.ReadFull(reader, element)
		if err != nil {
			return err
		}
		err = unmarshaler.UnmarshalBinary(element)
		if err != nil {
			return err
		}
	}
	return nil
}
