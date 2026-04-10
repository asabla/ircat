package server

import (
	"fmt"
	"sync/atomic"

	"github.com/asabla/ircat/internal/protocol"
)

// batchSeq is a process-global counter for generating unique batch
// reference tags.
var batchSeq atomic.Uint64

// nextBatchRef returns a unique batch reference string suitable for
// the IRCv3 batch protocol.
func nextBatchRef() string {
	return fmt.Sprintf("ircat%d", batchSeq.Add(1))
}

// startBatch sends a BATCH +ref line to the client, opening a
// batch of the given type.
func (c *Conn) startBatch(ref, batchType string, params ...string) {
	p := make([]string, 0, 2+len(params))
	p = append(p, "+"+ref, batchType)
	p = append(p, params...)
	c.send(&protocol.Message{
		Prefix:  c.server.cfg.Server.Name,
		Command: "BATCH",
		Params:  p,
	})
}

// endBatch sends a BATCH -ref line, closing the batch.
func (c *Conn) endBatch(ref string) {
	c.send(&protocol.Message{
		Prefix:  c.server.cfg.Server.Name,
		Command: "BATCH",
		Params:  []string{"-" + ref},
	})
}
