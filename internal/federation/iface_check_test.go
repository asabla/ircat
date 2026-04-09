package federation

import "time"

// Compile-time assertion that *Link satisfies the
// dashboardLinkRow shape that internal/server type-asserts. The
// shape is duplicated rather than imported because internal/server
// cannot import internal/federation either way (cycle), and a
// missed method on either side would otherwise only show up at
// runtime as a silent zero from the type-assertion fall-through.
var _ interface {
	StateString() string
	PeerName() string
	SentMessages() uint64
	SentBytes() uint64
	RecvMessages() uint64
	RecvBytes() uint64
	OpenedAt() time.Time
} = (*Link)(nil)
