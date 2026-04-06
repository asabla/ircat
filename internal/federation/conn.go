package federation

import (
	"bufio"
	"net"
	"time"

	"github.com/asabla/ircat/internal/protocol"
)

// WrapConnRead turns a net.Conn into a channel of parsed
// [protocol.Message] values. The channel closes when the conn
// errors or hits EOF. Parse errors on individual lines are
// swallowed so a peer sending a garbage line does not kill the
// whole link.
//
// Returns the channel and a closer; the caller is responsible for
// closing the conn itself (this helper owns a goroutine that
// reads until error).
func WrapConnRead(c net.Conn) <-chan *protocol.Message {
	out := make(chan *protocol.Message, 64)
	go func() {
		defer close(out)
		r := bufio.NewReaderSize(c, 4096)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			msg, perr := protocol.Parse(line)
			if perr != nil {
				continue
			}
			out <- msg
		}
	}()
	return out
}

// WrapConnWrite returns a writer closure suitable for passing to
// [Link.Run] as the lineWriter argument. It encodes each message
// via protocol.Message.Bytes and writes to c with a 5s deadline.
func WrapConnWrite(c net.Conn) func(msg *protocol.Message) error {
	return func(msg *protocol.Message) error {
		data, err := msg.Bytes()
		if err != nil {
			return err
		}
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err = c.Write(data)
		return err
	}
}
